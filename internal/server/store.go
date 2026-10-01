package server

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var (
	errNotFound = errors.New("not found")
	errTaken    = errors.New("taken by another account")
)

type Store struct {
	pool *pgxpool.Pool
}

func OpenStore(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 20
	var pool *pgxpool.Pool
	// The database may still be starting (docker compose); retry for a while.
	for attempt := 0; ; attempt++ {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				break
			}
			pool.Close()
		}
		if attempt >= 30 {
			return nil, fmt.Errorf("connect to database: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Migrate applies embedded migrations in lexical order, once each.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `select pg_advisory_lock(727274)`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `select pg_advisory_unlock(727274)`)

	if _, err := conn.Exec(ctx, `create table if not exists tund_migrations (version text primary key, applied_at timestamptz not null default now())`); err != nil {
		return err
	}
	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		version := strings.TrimPrefix(name, "migrations/")
		var exists bool
		if err := conn.QueryRow(ctx, `select exists(select 1 from tund_migrations where version=$1)`, version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sql, err := migrationsFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, `insert into tund_migrations(version) values ($1)`, version); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		logf("applied migration %s", version)
	}
	return nil
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

type Account struct {
	UserID        string
	TokenID       string
	Email         string
	IsAdmin       bool
	Disabled      bool
	Trusted       bool
	EmailVerified bool
}

func (s *Store) AuthenticateToken(ctx context.Context, token string) (*Account, error) {
	var a Account
	err := s.pool.QueryRow(ctx, `
		update authtokens t set last_used_at = now()
		from users u
		where t.token_hash = $1 and u.id = t.user_id
		returning t.user_id::text, t.id::text, u.email, u.is_admin, u.disabled_at is not null, u.trusted, u.email_verified_at is not null`, sha256Hex(token)).
		Scan(&a.UserID, &a.TokenID, &a.Email, &a.IsAdmin, &a.Disabled, &a.Trusted, &a.EmailVerified)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	return &a, err
}

// CloseStale marks tunnels and sessions left open by a previous process as ended.
// CloseStale ends tunnels and sessions this node left open (a previous run
// of the same node, or rows from before nodes existed).
func (s *Store) CloseStale(ctx context.Context, node string) error {
	_, err := s.pool.Exec(ctx, `update tunnels set ended_at = now() where ended_at is null and (node = $1 or node = '')`, node)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `update agent_sessions set disconnected_at = now() where disconnected_at is null and (node = $1 or node = '')`, node)
	return err
}

// errInUse: an online tunnel on another node holds the hostname or port.
type errInUse struct {
	Node, UserID, TunnelID string
}

func (e *errInUse) Error() string { return "in use on node " + e.Node }

type AgentSessionRow struct {
	UserID, TokenID, ClientVersion, ClientOS, Hostname, RemoteAddr string
	Node                                                           string
}

func (s *Store) CreateAgentSession(ctx context.Context, r AgentSessionRow) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		insert into agent_sessions (user_id, authtoken_id, client_version, client_os, hostname, remote_addr, node)
		values ($1, nullif($2,'')::uuid, $3, $4, $5, $6, $7) returning id::text`,
		r.UserID, r.TokenID, r.ClientVersion, r.ClientOS, r.Hostname, r.RemoteAddr, r.Node).Scan(&id)
	return id, err
}

func (s *Store) EndAgentSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `update agent_sessions set disconnected_at = now() where id = $1`, id)
	return err
}

type TunnelRow struct {
	SessionID, UserID, Name, Hostname, PublicURL, LocalAddr, AuthMode string
	Proto                                                             string
	RemotePort                                                        int
	Node                                                              string
}

func (s *Store) CreateTunnel(ctx context.Context, r TunnelRow) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	// This node's registry guarantees local exclusivity: leftover rows of this
	// node, of pre-cluster rows or of dead nodes are ended. A live tunnel on
	// another node is reported as errInUse.
	if _, err := tx.Exec(ctx, `update tunnels set ended_at = now()
		where ended_at is null and (hostname = $1 or (proto = 'tcp' and $3 > 0 and remote_port = $3))
		and (node = $2 or node = '' or node in (select name from nodes where last_seen < now() - interval '45 seconds'))`,
		r.Hostname, r.Node, r.RemotePort); err != nil {
		return "", err
	}
	var holder errInUse
	err = tx.QueryRow(ctx, `select node, user_id::text, id::text from tunnels
		where ended_at is null and (hostname = $1 or (proto = 'tcp' and $2 > 0 and remote_port = $2)) limit 1`,
		r.Hostname, r.RemotePort).Scan(&holder.Node, &holder.UserID, &holder.TunnelID)
	if err == nil {
		return "", &holder
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `
		insert into tunnels (agent_session_id, user_id, name, hostname, public_url, local_addr, auth_mode, proto, remote_port, node)
		values ($1, $2, $3, $4, $5, $6, $7, $8, nullif($9, 0), $10) returning id::text`,
		r.SessionID, r.UserID, r.Name, r.Hostname, r.PublicURL, r.LocalAddr, r.AuthMode, r.Proto, r.RemotePort, r.Node).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", &errInUse{}
		}
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) EndTunnel(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `update tunnels set ended_at = now() where id = $1 and ended_at is null`, id)
	return err
}

func (s *Store) EndTunnelOwned(ctx context.Context, id, userID string) error {
	_, err := s.pool.Exec(ctx, `update tunnels set ended_at = now() where id = $1 and user_id = $2 and ended_at is null`, id, userID)
	return err
}

func (s *Store) SetTunnelAuthMode(ctx context.Context, id, mode string) error {
	_, err := s.pool.Exec(ctx, `update tunnels set auth_mode = $2 where id = $1`, id, mode)
	return err
}

// LastSeen returns when a tunnel on hostname last ended (for the offline page).
func (s *Store) LastSeen(ctx context.Context, host string) (time.Time, bool) {
	var t time.Time
	err := s.pool.QueryRow(ctx, `select coalesce(ended_at, started_at) from tunnels where hostname = $1 order by started_at desc limit 1`, host).Scan(&t)
	return t, err == nil
}

func (s *Store) Notify(ctx context.Context, channel string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if _, err := s.pool.Exec(ctx, `select pg_notify($1, $2)`, channel, string(b)); err != nil {
		logf("notify %s: %v", channel, err)
	}
}

type Domain struct {
	ID, UserID, Hostname, Kind string
	TeamID                     string // non-empty: owned by this team
	Approval                   string // custom domains: approved | pending | rejected
	Verified                   bool
	AuthMode                   string
	AuthPasswordHash           string
	AuthOIDCProviderID         string
	AuthOIDCAllow              []string
}

const domainCols = `id::text, user_id::text, hostname, kind, verified_at is not null, auth_mode,
	coalesce(auth_password_hash, ''), coalesce(auth_oidc_provider_id::text, ''), auth_oidc_allow, coalesce(team_id::text, ''), approval`

func scanDomain(row pgx.Row) (*Domain, error) {
	var d Domain
	err := row.Scan(&d.ID, &d.UserID, &d.Hostname, &d.Kind, &d.Verified, &d.AuthMode, &d.AuthPasswordHash, &d.AuthOIDCProviderID, &d.AuthOIDCAllow, &d.TeamID, &d.Approval)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ResolveDomain finds the domain row governing host: an exact match first,
// then a wildcard entry for its parent ("*.parent").
func (s *Store) ResolveDomain(ctx context.Context, host string) (*Domain, error) {
	d, err := scanDomain(s.pool.QueryRow(ctx, `select `+domainCols+` from domains where hostname = $1`, host))
	if err == nil || !errors.Is(err, errNotFound) {
		return d, err
	}
	if i := strings.IndexByte(host, '.'); i > 0 {
		return scanDomain(s.pool.QueryRow(ctx, `select `+domainCols+` from domains where hostname = $1`, "*"+host[i:]))
	}
	return nil, errNotFound
}

// DefaultStatic returns the account's default static hostname.
func (s *Store) DefaultStatic(ctx context.Context, userID string) (*Domain, error) {
	return scanDomain(s.pool.QueryRow(ctx, `select `+domainCols+` from domains where user_id = $1 and team_id is null and kind = 'subdomain' and is_default`, userID))
}

func (s *Store) CountStatic(ctx context.Context, userID string) (int, error) {
	var n int
	// Static addresses: personal static hostnames and reserved TCP ports.
	err := s.pool.QueryRow(ctx, `select
		(select count(*) from domains where user_id = $1 and team_id is null and kind = 'subdomain') +
		(select count(*) from tcp_reservations where user_id = $1 and team_id is null)`, userID).Scan(&n)
	return n, err
}

// PinStatic makes host a static hostname of the user, and the default if the
// user has none yet. errTaken if another account already pinned it.
func (s *Store) PinStatic(ctx context.Context, userID, host string) (*Domain, error) {
	for attempt := 0; ; attempt++ {
		d, err := scanDomain(s.pool.QueryRow(ctx, `
			insert into domains (user_id, hostname, kind, verified_at, is_default)
			values ($1, $2, 'subdomain', now(),
				$3 and not exists (select 1 from domains where user_id = $1 and is_default))
			on conflict (hostname) do nothing
			returning `+domainCols, userID, host, attempt == 0))
		if err == nil {
			return d, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && attempt == 0 {
			continue // someone set a default concurrently; pin without it
		}
		if !errors.Is(err, errNotFound) {
			return nil, err
		}
		// Conflict on hostname: fine if it is already ours.
		d, err = s.ResolveDomain(ctx, host)
		if err != nil {
			return nil, err
		}
		if d.UserID != userID || d.Hostname != host {
			return nil, errTaken
		}
		return d, nil
	}
}

type OIDCProvider struct {
	ID, UserID, Name, Slug, Issuer, ClientID, ClientSecret, Scopes string
	TeamID, TeamSlug                                               string
}

const providerCols = `p.id::text, p.user_id::text, p.name, p.slug, p.issuer, p.client_id, p.client_secret, p.scopes,
	coalesce(p.team_id::text, ''), coalesce(t.slug, '')`
const providerFrom = ` from oidc_providers p left join teams t on t.id = p.team_id `

func scanProvider(row pgx.Row) (*OIDCProvider, error) {
	var p OIDCProvider
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Slug, &p.Issuer, &p.ClientID, &p.ClientSecret, &p.Scopes, &p.TeamID, &p.TeamSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) OIDCProvider(ctx context.Context, id string) (*OIDCProvider, error) {
	return scanProvider(s.pool.QueryRow(ctx, `select `+providerCols+providerFrom+`where p.id = $1`, id))
}

// PersonalProvider finds one of the user's own (non-team) providers.
func (s *Store) PersonalProvider(ctx context.Context, userID, slug string) (*OIDCProvider, error) {
	return scanProvider(s.pool.QueryRow(ctx, `select `+providerCols+providerFrom+`where p.user_id = $1 and p.team_id is null and p.slug = $2`, userID, slug))
}

// TeamProviders lists providers with this slug in teams the user belongs to;
// teamSlug narrows it to one team.
func (s *Store) TeamProviders(ctx context.Context, userID, teamSlug, slug string) ([]*OIDCProvider, error) {
	rows, err := s.pool.Query(ctx, `select `+providerCols+providerFrom+`
		join team_members m on m.team_id = p.team_id and m.user_id = $1
		where p.slug = $2 and ($3 = '' or t.slug = $3) order by t.slug`, userID, slug, teamSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OIDCProvider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) IsTeamMember(ctx context.Context, teamID, userID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `select exists(select 1 from team_members where team_id = $1 and user_id = $2)`, teamID, userID).Scan(&ok)
	return ok, err
}

// ShareTeamDomain reports whether both users belong to the team owning host.
func (s *Store) ShareTeamDomain(ctx context.Context, host, userA, userB string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `select exists(
		select 1 from domains d
		join team_members a on a.team_id = d.team_id and a.user_id = $2
		join team_members b on b.team_id = d.team_id and b.user_id = $3
		where d.hostname = $1 or (left(d.hostname, 2) = '*.' and substr(d.hostname, 3) = substr($1, strpos($1, '.') + 1)))`,
		host, userA, userB).Scan(&ok)
	return ok, err
}

type TeamInfo struct{ Slug, Name, Role string }

func (s *Store) ListTeams(ctx context.Context, userID string) ([]TeamInfo, error) {
	rows, err := s.pool.Query(ctx, `select t.slug, t.name, m.role from teams t join team_members m on m.team_id = t.id where m.user_id = $1 order by t.name`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (TeamInfo, error) {
		var t TeamInfo
		return t, r.Scan(&t.Slug, &t.Name, &t.Role)
	})
}

// RequestRecord is one captured exchange.
type RequestRecord struct {
	ID                string
	TunnelID          string
	UserID            string
	Hostname          string
	Method            string
	Path              string
	Proto             string
	RemoteAddr        string
	ReqHeaders        http.Header
	ReqBody           []byte
	ReqBodySize       int64
	ReqBodyTruncated  bool
	Status            int
	RespHeaders       http.Header
	RespBody          []byte
	RespBodySize      int64
	RespBodyTruncated bool
	TTFB              time.Duration
	Duration          time.Duration
	Error             string
	ReplayOf          string
	StartedAt         time.Time
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func headerJSON(h http.Header) []byte {
	if h == nil {
		h = http.Header{}
	}
	b, _ := json.Marshal(h)
	return b
}

func (s *Store) InsertRequests(ctx context.Context, recs []*RequestRecord) error {
	batch := &pgx.Batch{}
	for _, r := range recs {
		batch.Queue(`
			insert into requests (id, tunnel_id, user_id, hostname, method, path, proto, remote_addr,
				req_headers, req_body, req_body_size, req_body_truncated,
				status, resp_headers, resp_body, resp_body_size, resp_body_truncated,
				ttfb_ms, duration_ms, error, replay_of, started_at)
			values ($1, nullif($2,'')::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, nullif($21,'')::uuid, $22)`,
			r.ID, r.TunnelID, r.UserID, r.Hostname, r.Method, r.Path, r.Proto, r.RemoteAddr,
			headerJSON(r.ReqHeaders), r.ReqBody, r.ReqBodySize, r.ReqBodyTruncated,
			r.Status, headerJSON(r.RespHeaders), r.RespBody, r.RespBodySize, r.RespBodyTruncated,
			ms(r.TTFB), ms(r.Duration), r.Error, r.ReplayOf, r.StartedAt)
	}
	for _, r := range recs {
		b, _ := json.Marshal(map[string]string{"id": r.ID, "user_id": r.UserID, "tunnel_id": r.TunnelID, "hostname": r.Hostname})
		batch.Queue(`select pg_notify('tund_requests', $1)`, string(b))
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

// TCPReservation reports who reserved a TCP port.
func (s *Store) TCPReservation(ctx context.Context, port int) (userID, teamID string, reserved bool, err error) {
	err = s.pool.QueryRow(ctx, `select user_id::text, coalesce(team_id::text, '') from tcp_reservations where port = $1`, port).Scan(&userID, &teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	return userID, teamID, err == nil, err
}

// ReservedPorts lists the ports reserved by the user or one of the user's teams.
func (s *Store) ReservedPorts(ctx context.Context, userID string) ([]int, error) {
	rows, err := s.pool.Query(ctx, `select port from tcp_reservations
		where (user_id = $1 and team_id is null) or team_id in (select team_id from team_members where user_id = $1)
		order by team_id nulls first, created_at`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}

// ReserveTCP reserves port for the user; errTaken if someone else has it.
func (s *Store) ReserveTCP(ctx context.Context, userID string, port int) error {
	tag, err := s.pool.Exec(ctx, `insert into tcp_reservations (port, user_id) values ($1, $2) on conflict (port) do nothing`, port, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		owner, team, _, err := s.TCPReservation(ctx, port)
		if err != nil {
			return err
		}
		if owner != userID || team != "" {
			return errTaken
		}
	}
	return nil
}

// ConnRecord is one finished TCP/TLS connection.
type ConnRecord struct {
	ID, TunnelID, UserID, Proto, Address, RemoteAddr, Error string
	BytesIn, BytesOut                                       int64
	Duration                                                time.Duration
	StartedAt                                               time.Time
}

func (s *Store) InsertConnections(ctx context.Context, recs []*ConnRecord) error {
	batch := &pgx.Batch{}
	for _, c := range recs {
		batch.Queue(`insert into connections (id, tunnel_id, user_id, proto, address, remote_addr, bytes_in, bytes_out, duration_ms, error, started_at)
			values ($1, nullif($2,'')::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			c.ID, c.TunnelID, c.UserID, c.Proto, c.Address, c.RemoteAddr, c.BytesIn, c.BytesOut, ms(c.Duration), c.Error, c.StartedAt)
	}
	for _, c := range recs {
		b, _ := json.Marshal(map[string]string{"id": c.ID, "user_id": c.UserID, "tunnel_id": c.TunnelID, "address": c.Address})
		batch.Queue(`select pg_notify('tund_connections', $1)`, string(b))
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

type ConnFilter struct {
	TunnelID, Address string
	Before            time.Time
	Limit             int
}

// ListConnections returns connections of the user's tunnels and of team-owned
// addresses (team static hostnames / team-reserved ports) of the user's teams.
func (s *Store) ListConnections(ctx context.Context, userID string, f ConnFilter) ([]*ConnRecord, error) {
	q := `select c.id::text, coalesce(c.tunnel_id::text, ''), c.user_id::text, c.proto, c.address, c.remote_addr, c.error,
		c.bytes_in, c.bytes_out, c.duration_ms, c.started_at
		from connections c where (c.user_id = $1
			or exists (select 1 from domains d join team_members m on m.team_id = d.team_id and m.user_id = $1 where d.hostname = c.address)
			or exists (select 1 from tcp_reservations tr join team_members m on m.team_id = tr.team_id and m.user_id = $1
				where c.proto = 'tcp' and c.address like '%:' || tr.port::text))`
	args := []any{userID}
	if f.TunnelID != "" {
		args = append(args, f.TunnelID)
		q += fmt.Sprintf(" and c.tunnel_id::text = $%d", len(args))
	}
	if f.Address != "" {
		args = append(args, f.Address)
		q += fmt.Sprintf(" and c.address = $%d", len(args))
	}
	if !f.Before.IsZero() {
		args = append(args, f.Before)
		q += fmt.Sprintf(" and c.started_at < $%d", len(args))
	}
	q += fmt.Sprintf(" order by c.started_at desc limit %d", f.Limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ConnRecord
	for rows.Next() {
		var c ConnRecord
		var durMS float64
		if err := rows.Scan(&c.ID, &c.TunnelID, &c.UserID, &c.Proto, &c.Address, &c.RemoteAddr, &c.Error, &c.BytesIn, &c.BytesOut, &durMS, &c.StartedAt); err != nil {
			return nil, err
		}
		c.Duration = time.Duration(durMS * float64(time.Millisecond))
		out = append(out, &c)
	}
	return out, rows.Err()
}

type UserLimitRow struct {
	IsAdmin         bool
	BandwidthKbps   *int
	TransferQuotaGB *int
	CustomDomains   *bool // nil: the instance setting
}

func (s *Store) UserLimits(ctx context.Context, userID string) (*UserLimitRow, error) {
	var l UserLimitRow
	err := s.pool.QueryRow(ctx, `select is_admin, bandwidth_kbps, transfer_quota_gb, custom_domains from users where id = $1`, userID).
		Scan(&l.IsAdmin, &l.BandwidthKbps, &l.TransferQuotaGB, &l.CustomDomains)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	return &l, err
}

type Usage struct {
	BytesIn, BytesOut, Requests, Connections int64
}

func (s *Store) MonthUsage(ctx context.Context, userID string, start time.Time) (Usage, error) {
	var u Usage
	err := s.pool.QueryRow(ctx, `select coalesce(sum(bytes_in), 0), coalesce(sum(bytes_out), 0), coalesce(sum(requests), 0), coalesce(sum(connections), 0)
		from usage_daily where user_id = $1 and day >= $2::date`, userID, start.Format("2006-01-02")).
		Scan(&u.BytesIn, &u.BytesOut, &u.Requests, &u.Connections)
	return u, err
}

func (s *Store) AddUsage(ctx context.Context, userID, day string, in, out, requests, conns int64) error {
	_, err := s.pool.Exec(ctx, `insert into usage_daily (user_id, day, bytes_in, bytes_out, requests, connections)
		values ($1, $2::date, $3, $4, $5, $6)
		on conflict (user_id, day) do update set bytes_in = usage_daily.bytes_in + excluded.bytes_in,
			bytes_out = usage_daily.bytes_out + excluded.bytes_out, requests = usage_daily.requests + excluded.requests,
			connections = usage_daily.connections + excluded.connections`, userID, day, in, out, requests, conns)
	return err
}

// BlockedHosts returns hostname → reason for every blocked hostname.
func (s *Store) BlockedHosts(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `select hostname, reason from blocked_hosts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var h, r string
		if err := rows.Scan(&h, &r); err != nil {
			return nil, err
		}
		out[h] = r
	}
	return out, rows.Err()
}

func (s *Store) BlockHost(ctx context.Context, host, reason string) error {
	_, err := s.pool.Exec(ctx, `insert into blocked_hosts (hostname, reason) values ($1, $2) on conflict (hostname) do nothing`, host, reason)
	return err
}

func (s *Store) FlagUser(ctx context.Context, userID, reason string) error {
	_, err := s.pool.Exec(ctx, `update users set flagged_at = coalesce(flagged_at, now()), flag_reason = $2 where id = $1`, userID, reason)
	return err
}

type AbuseReport struct {
	Hostname, URL, Category, Source, Description, UserID, TunnelID string
	Details                                                        map[string]any
}

func (s *Store) CreateAbuseReport(ctx context.Context, r AbuseReport) (string, error) {
	details, _ := json.Marshal(r.Details)
	var id string
	err := s.pool.QueryRow(ctx, `insert into abuse_reports (hostname, url, category, source, description, details, user_id, tunnel_id)
		values ($1, $2, $3, $4, $5, $6, nullif($7, '')::uuid, nullif($8, '')::uuid) returning id::text`,
		r.Hostname, r.URL, r.Category, r.Source, r.Description, details, r.UserID, r.TunnelID).Scan(&id)
	return id, err
}

// --- cluster ---

func (s *Store) UpsertNode(ctx context.Context, n nodeInfo, m nodeMetrics) error {
	_, err := s.pool.Exec(ctx, `insert into nodes (name, role, region, relay_url, relay_cert_sha256, version, started_at, last_seen,
			public_ip, capacity_mbps, cpus, cpu_pct, load1, mem_total, mem_used, net_in_rate, net_out_rate, tunnel_rate, sessions, tunnels, metrics_at)
		values ($1, $2, $3, $4, $5, $6, now(), now(), $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, now())
		on conflict (name) do update set role = excluded.role, region = excluded.region, relay_url = excluded.relay_url,
			relay_cert_sha256 = excluded.relay_cert_sha256, version = excluded.version, last_seen = now(),
			started_at = case when nodes.relay_cert_sha256 = excluded.relay_cert_sha256 then nodes.started_at else now() end,
			public_ip = excluded.public_ip, capacity_mbps = excluded.capacity_mbps, cpus = excluded.cpus, cpu_pct = excluded.cpu_pct,
			load1 = excluded.load1, mem_total = excluded.mem_total, mem_used = excluded.mem_used, net_in_rate = excluded.net_in_rate,
			net_out_rate = excluded.net_out_rate, tunnel_rate = excluded.tunnel_rate, sessions = excluded.sessions,
			tunnels = excluded.tunnels, metrics_at = now()`,
		n.Name, n.Role, n.Region, n.RelayURL, n.CertSHA, n.Version,
		m.PublicIP, m.CapacityMbps, m.CPUs, m.CPUPct, m.Load1, m.MemTotal, m.MemUsed, m.NetInRate, m.NetOutRate, m.TunnelRate, m.Sessions, m.Tunnels)
	return err
}

func (s *Store) Nodes(ctx context.Context) ([]nodeInfo, error) {
	rows, err := s.pool.Query(ctx, `select name, role, region, relay_url, relay_cert_sha256, version, last_seen,
		public_ip, capacity_mbps, cpus, cpu_pct, load1, mem_total, mem_used, net_in_rate, net_out_rate, tunnel_rate, sessions, tunnels, metrics_at
		from nodes order by name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (nodeInfo, error) {
		var n nodeInfo
		m := &n.Metrics
		return n, r.Scan(&n.Name, &n.Role, &n.Region, &n.RelayURL, &n.CertSHA, &n.Version, &n.LastSeen,
			&m.PublicIP, &m.CapacityMbps, &m.CPUs, &m.CPUPct, &m.Load1, &m.MemTotal, &m.MemUsed, &m.NetInRate, &m.NetOutRate,
			&m.TunnelRate, &m.Sessions, &m.Tunnels, &n.MetricsAt)
	})
}

// EndDeadNodes ends tunnels and sessions of nodes that stopped heartbeating.
func (s *Store) EndDeadNodes(ctx context.Context, after time.Duration) error {
	secs := fmt.Sprintf("%d seconds", int(after.Seconds()))
	if _, err := s.pool.Exec(ctx, `update tunnels set ended_at = now() where ended_at is null and node in
		(select name from nodes where last_seen < now() - $1::interval)`, secs); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `update agent_sessions set disconnected_at = now() where disconnected_at is null and node in
		(select name from nodes where last_seen < now() - $1::interval)`, secs)
	return err
}

// TunnelNode finds the node of the online tunnel for a hostname or "tcp:<port>".
func (s *Store) TunnelNode(ctx context.Context, key string) (node, proto string, err error) {
	if port, ok := strings.CutPrefix(key, "tcp:"); ok {
		err = s.pool.QueryRow(ctx, `select node, proto from tunnels where ended_at is null and proto = 'tcp' and remote_port = $1::int`, port).Scan(&node, &proto)
	} else {
		err = s.pool.QueryRow(ctx, `select node, proto from tunnels where ended_at is null and hostname = $1`, key).Scan(&node, &proto)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", errNotFound
	}
	return node, proto, err
}

// TunnelsPerNode counts online tunnels per node.
func (s *Store) TunnelsPerNode(ctx context.Context) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `select node, count(*) from tunnels where ended_at is null group by node`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var n string
		var c int
		if err := rows.Scan(&n, &c); err != nil {
			return nil, err
		}
		out[n] = c
	}
	return out, rows.Err()
}

// TunnelNodeByID returns the node holding an online tunnel.
func (s *Store) TunnelNodeByID(ctx context.Context, id string) (string, error) {
	var node string
	err := s.pool.QueryRow(ctx, `select node from tunnels where id = $1 and ended_at is null`, id).Scan(&node)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errNotFound
	}
	return node, err
}

// RemoteTCPTunnels maps ports of online TCP tunnels on other live nodes to their node.
func (s *Store) RemoteTCPTunnels(ctx context.Context, self string, after time.Duration) (map[int]string, error) {
	rows, err := s.pool.Query(ctx, `select t.remote_port, t.node from tunnels t join nodes n on n.name = t.node
		where t.ended_at is null and t.proto = 'tcp' and t.node <> $1 and n.last_seen > now() - $2::interval`,
		self, fmt.Sprintf("%d seconds", int(after.Seconds())))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var port int
		var node string
		if err := rows.Scan(&port, &node); err != nil {
			return nil, err
		}
		out[port] = node
	}
	return out, rows.Err()
}

type OnlineTunnel struct {
	ID, Name, Proto, Hostname, PublicURL, LocalAddr, AuthMode, Node string
	RemotePort                                                      int
	Static                                                          bool
	StartedAt                                                       time.Time
	ClientHostname, ClientOS, ClientVersion                         string
}

// OnlineTunnels lists a user's online tunnels on all nodes.
func (s *Store) OnlineTunnels(ctx context.Context, userID string) ([]OnlineTunnel, error) {
	rows, err := s.pool.Query(ctx, `select t.id::text, t.name, t.proto, t.hostname, t.public_url, t.local_addr, t.auth_mode, t.node,
		coalesce(t.remote_port, 0),
		exists(select 1 from domains d where d.hostname = t.hostname and d.kind = 'subdomain')
			or exists(select 1 from tcp_reservations tr where t.proto = 'tcp' and tr.port = t.remote_port),
		t.started_at, a.hostname, a.client_os, a.client_version
		from tunnels t join agent_sessions a on a.id = t.agent_session_id
		where t.user_id = $1 and t.ended_at is null order by t.started_at`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (OnlineTunnel, error) {
		var t OnlineTunnel
		return t, r.Scan(&t.ID, &t.Name, &t.Proto, &t.Hostname, &t.PublicURL, &t.LocalAddr, &t.AuthMode, &t.Node,
			&t.RemotePort, &t.Static, &t.StartedAt, &t.ClientHostname, &t.ClientOS, &t.ClientVersion)
	})
}

// Settings returns all rows of the settings table.
func (s *Store) Settings(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := s.pool.Query(ctx, `select key, value from settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// UserTrusted reports whether a user's tunnels skip the browser warning.
func (s *Store) UserTrusted(ctx context.Context, id string) (bool, error) {
	var t bool
	err := s.pool.QueryRow(ctx, `select is_admin or trusted from users where id = $1`, id).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, errNotFound
	}
	return t, err
}

type UserInfo struct {
	ID, Email, Name string
	IsAdmin         bool
}

func (s *Store) User(ctx context.Context, id string) (*UserInfo, error) {
	var u UserInfo
	err := s.pool.QueryRow(ctx, `select id::text, email, name, is_admin from users where id = $1`, id).Scan(&u.ID, &u.Email, &u.Name, &u.IsAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	return &u, err
}

type StaticHostname struct {
	Hostname  string
	IsDefault bool
}

func (s *Store) ListStatic(ctx context.Context, userID string) ([]StaticHostname, error) {
	rows, err := s.pool.Query(ctx, `select hostname, is_default from domains where user_id = $1 and kind = 'subdomain' order by is_default desc, created_at`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (StaticHostname, error) {
		var h StaticHostname
		return h, r.Scan(&h.Hostname, &h.IsDefault)
	})
}

// RequestFilter narrows ListRequests; zero values mean "any".
type RequestFilter struct {
	Hostname, TunnelID, Method, StatusClass, Path string
	Before                                        time.Time
	Limit                                         int
}

type RequestRow struct {
	ID, TunnelID, Hostname, Method, Path, Proto, RemoteAddr, Error, ReplayOf string
	Status                                                                   int
	ReqBodySize, RespBodySize                                                int64
	TTFBMS, DurationMS                                                       float64
	StartedAt                                                                time.Time

	// Only filled by RequestDetail.
	ReqHeaders, RespHeaders             http.Header
	ReqBody, RespBody                   []byte
	ReqBodyTruncated, RespBodyTruncated bool
}

const requestSummaryCols = `r.id::text, coalesce(r.tunnel_id::text, ''), r.hostname, r.method, r.path, r.proto, r.remote_addr, r.error,
	coalesce(r.replay_of::text, ''), r.status, r.req_body_size, r.resp_body_size, r.ttfb_ms, r.duration_ms, r.started_at`

// visibleToUser ($1 = user id) matches requests of the user's own tunnels and
// requests on hostnames owned by one of the user's teams.
const visibleToUser = `(r.user_id = $1 or exists (
	select 1 from domains d join team_members m on m.team_id = d.team_id and m.user_id = $1
	where d.hostname = r.hostname or (left(d.hostname, 2) = '*.' and substr(d.hostname, 3) = substr(r.hostname, strpos(r.hostname, '.') + 1))))`

func scanRequestSummary(row pgx.Row, r *RequestRow, extra ...any) error {
	return row.Scan(append([]any{&r.ID, &r.TunnelID, &r.Hostname, &r.Method, &r.Path, &r.Proto, &r.RemoteAddr, &r.Error,
		&r.ReplayOf, &r.Status, &r.ReqBodySize, &r.RespBodySize, &r.TTFBMS, &r.DurationMS, &r.StartedAt}, extra...)...)
}

func (s *Store) ListRequests(ctx context.Context, userID string, f RequestFilter) ([]RequestRow, error) {
	q := `select ` + requestSummaryCols + ` from requests r where ` + visibleToUser
	args := []any{userID}
	add := func(cond string, v any) {
		args = append(args, v)
		q += fmt.Sprintf(" and "+cond, len(args))
	}
	if f.Hostname != "" {
		add("r.hostname = $%d", f.Hostname)
	}
	if f.TunnelID != "" {
		add("r.tunnel_id::text = $%d", f.TunnelID)
	}
	if f.Method != "" {
		add("r.method = $%d", strings.ToUpper(f.Method))
	}
	if f.Path != "" {
		add("strpos(r.path, $%d) > 0", f.Path)
	}
	switch f.StatusClass {
	case "2xx":
		q += " and ((r.status >= 200 and r.status < 300) or r.status = 101)"
	case "3xx":
		q += " and r.status >= 300 and r.status < 400"
	case "4xx":
		q += " and r.status >= 400 and r.status < 500"
	case "5xx":
		q += " and (r.status >= 500 or r.status = 0)"
	}
	if !f.Before.IsZero() {
		add("r.started_at < $%d", f.Before)
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	q += fmt.Sprintf(" order by r.started_at desc limit %d", f.Limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestRow
	for rows.Next() {
		var r RequestRow
		if err := scanRequestSummary(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RequestDetail(ctx context.Context, userID, id string) (*RequestRow, error) {
	var r RequestRow
	var reqH, respH []byte
	err := scanRequestSummary(s.pool.QueryRow(ctx, `select `+requestSummaryCols+`,
		r.req_headers, coalesce(r.req_body, ''::bytea), r.req_body_truncated,
		r.resp_headers, coalesce(r.resp_body, ''::bytea), r.resp_body_truncated
		from requests r where r.id = $2 and `+visibleToUser, userID, id), &r,
		&reqH, &r.ReqBody, &r.ReqBodyTruncated, &respH, &r.RespBody, &r.RespBodyTruncated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal(reqH, &r.ReqHeaders)
	json.Unmarshal(respH, &r.RespHeaders)
	return &r, nil
}

// StoredRequest is what replay needs from a captured request.
type StoredRequest struct {
	ID, UserID, Hostname, Method, Path string
	Headers                            http.Header
	Body                               []byte
	BodyTruncated                      bool
}

func (s *Store) Request(ctx context.Context, id, userID string) (*StoredRequest, error) {
	var r StoredRequest
	var hdr []byte
	err := s.pool.QueryRow(ctx, `
		select r.id::text, r.user_id::text, r.hostname, r.method, r.path, r.req_headers, coalesce(r.req_body, ''::bytea), r.req_body_truncated
		from requests r where r.id = $2 and `+visibleToUser, userID, id).
		Scan(&r.ID, &r.UserID, &r.Hostname, &r.Method, &r.Path, &hdr, &r.Body, &r.BodyTruncated)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(hdr, &r.Headers); err != nil {
		return nil, err
	}
	return &r, nil
}

// Prune removes captured data older than the retention window.
func (s *Store) Prune(ctx context.Context, days int) error {
	if days <= 0 {
		return nil
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	if _, err := s.pool.Exec(ctx, `delete from requests where started_at < $1`, cutoff); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `delete from connections where started_at < $1`, cutoff); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `delete from agent_sessions where disconnected_at < $1`, cutoff); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `delete from device_codes where expires_at < now() - interval '1 hour'`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `delete from sessions where expires_at < now()`)
	return err
}

type DeviceCodeRow struct {
	DeviceCodeHash, UserCode, TokenHash, TokenPrefix string
	ClientHostname, ClientOS, ClientIP               string
	ExpiresAt                                        time.Time
	CallbackPort                                     int // 0: plain device flow
	CallbackState                                    string
}

func (s *Store) CreateDeviceCode(ctx context.Context, r DeviceCodeRow) error {
	var port *int
	var state *string
	if r.CallbackPort != 0 {
		port, state = &r.CallbackPort, &r.CallbackState
	}
	_, err := s.pool.Exec(ctx, `
		insert into device_codes (device_code_hash, user_code, token_hash, token_prefix, client_hostname, client_os, client_ip, expires_at, callback_port, callback_state)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		r.DeviceCodeHash, r.UserCode, r.TokenHash, r.TokenPrefix, r.ClientHostname, r.ClientOS, r.ClientIP, r.ExpiresAt, port, state)
	return err
}

type DevicePoll struct {
	Status  string
	Account string
	Expired bool
}

// PollDeviceCode reports the state of a login request. Finished requests
// (approved, denied, expired) are deleted once reported.
func (s *Store) PollDeviceCode(ctx context.Context, deviceCodeHash string) (*DevicePoll, error) {
	var p DevicePoll
	err := s.pool.QueryRow(ctx, `
		select d.status, coalesce(u.email, ''), d.expires_at < now()
		from device_codes d left join users u on u.id = d.user_id
		where d.device_code_hash = $1`, deviceCodeHash).Scan(&p.Status, &p.Account, &p.Expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	// 'authorized' (a loopback login approved in the browser, waiting for the
	// CLI to redeem its callback code) still reads as pending here.
	if (p.Status != "pending" && p.Status != "authorized") || p.Expired {
		if p.Status == "approved" {
			p.Expired = false // approved in time; the token already exists
		}
		if _, err := s.pool.Exec(ctx, `delete from device_codes where device_code_hash = $1`, deviceCodeHash); err != nil {
			return nil, err
		}
	}
	return &p, nil
}

var (
	errDeviceExpired  = errors.New("device code expired")
	errDeviceDenied   = errors.New("device code denied")
	errDeviceCallback = errors.New("wrong or stale callback code")
)

// RedeemDeviceCallback creates the authtoken of a loopback login that was
// authorized in the dashboard, given the callback code the browser brought
// to the CLI, and returns the account's email. Redeeming the same code again
// succeeds without side effects (a reloaded callback page); the row itself is
// left for the CLI's poll or Prune.
func (s *Store) RedeemDeviceCallback(ctx context.Context, deviceCodeHash, callbackCodeHash string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var (
		id, status, codeHash, userID, email string
		tokenHash, tokenPrefix, hostname    string
		expired, disabled                   bool
	)
	err = tx.QueryRow(ctx, `
		select d.id::text, d.status, coalesce(d.callback_code_hash, ''), coalesce(d.user_id::text, ''), coalesce(u.email, ''),
		       d.token_hash, d.token_prefix, d.client_hostname, d.expires_at < now(), u.disabled_at is not null
		from device_codes d left join users u on u.id = d.user_id
		where d.device_code_hash = $1
		for update of d`, deviceCodeHash).
		Scan(&id, &status, &codeHash, &userID, &email, &tokenHash, &tokenPrefix, &hostname, &expired, &disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errNotFound
	}
	if err != nil {
		return "", err
	}
	switch {
	case status == "denied" || disabled:
		return "", errDeviceDenied
	case codeHash == "" || codeHash != callbackCodeHash:
		return "", errDeviceCallback
	case status == "approved":
		return email, nil
	case status != "authorized":
		return "", errDeviceCallback
	case expired:
		return "", errDeviceExpired
	}
	name := "CLI on " + hostname
	if hostname == "" {
		name = "CLI on unknown host"
	}
	var tokenID string
	if err := tx.QueryRow(ctx, `
		insert into authtokens (user_id, name, token_hash, token_prefix) values ($1, $2, $3, $4) returning id::text`,
		userID, truncate(name, 80), tokenHash, tokenPrefix).Scan(&tokenID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `update device_codes set status = 'approved', authtoken_id = $2 where id = $1`, id, tokenID); err != nil {
		return "", err
	}
	return email, tx.Commit(ctx)
}

// Listen runs fn for every notification on channel until ctx is done,
// reconnecting on errors.
func (s *Store) Listen(ctx context.Context, channel string, fn func(payload string)) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := func() error {
			conn, err := s.pool.Acquire(ctx)
			if err != nil {
				return err
			}
			defer conn.Release()
			if _, err := conn.Exec(ctx, `listen `+pgx.Identifier{channel}.Sanitize()); err != nil {
				return err
			}
			// Some events may have been missed while disconnected.
			fn("")
			backoff = time.Second
			for {
				n, err := conn.Conn().WaitForNotification(ctx)
				if err != nil {
					conn.Conn().Close(context.Background())
					return err
				}
				fn(n.Payload)
			}
		}()
		if ctx.Err() != nil {
			return
		}
		logf("listen %s: %v (retrying in %s)", channel, err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}
