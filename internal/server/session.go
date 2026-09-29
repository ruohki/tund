package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"tund/internal/protocol"
	"tund/internal/pwhash"
)

// AgentSession is one connected tund client.
type AgentSession struct {
	ID      string
	Account Account

	ClientHostname, ClientOS, ClientVersion string
	srv                                     *Server
	mux                                     *yamux.Session
	ctrl                                    *protocol.Control

	mu      sync.Mutex
	tunnels map[string]*Tunnel // by bind id
	closed  bool
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, "{\"error\":%q}\n", msg)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing authtoken; run `tund login` (or set TUND_AUTHTOKEN in CI)")
		return
	}
	acct, err := s.store.AuthenticateToken(r.Context(), token)
	if errors.Is(err, errNotFound) {
		writeJSONError(w, http.StatusUnauthorized, "invalid authtoken; create one in the dashboard at "+s.cfg.DashboardURL()+"/authtokens")
		return
	}
	if err != nil {
		logf("authenticate: %v", err)
		writeJSONError(w, http.StatusServiceUnavailable, "server error, try again")
		return
	}
	if acct.Disabled {
		writeJSONError(w, http.StatusForbidden, "account disabled; contact the administrator of "+s.cfg.DashboardHost)
		return
	}
	if s.emailVerificationRequired(acct) {
		writeJSONError(w, http.StatusForbidden, "verify your email address first: open the link we sent to "+acct.Email+" (or resend it from "+s.cfg.DashboardURL()+")")
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nc := websocket.NetConn(ctx, c, websocket.MessageBinary)
	mux, err := yamux.Server(nc, protocol.YamuxConfig())
	if err != nil {
		nc.Close()
		return
	}
	defer mux.Close()

	ctrlStream, err := acceptTimeout(mux, 15*time.Second)
	if err != nil {
		return
	}

	as := &AgentSession{
		Account: *acct, srv: s, mux: mux, ctrl: protocol.NewControl(ctrlStream), tunnels: map[string]*Tunnel{},
		ClientHostname: r.Header.Get(protocol.HeaderHostname),
		ClientOS:       r.Header.Get(protocol.HeaderOS),
		ClientVersion:  r.Header.Get(protocol.HeaderClient),
	}
	as.ID, err = s.store.CreateAgentSession(ctx, AgentSessionRow{
		UserID:        acct.UserID,
		TokenID:       acct.TokenID,
		ClientVersion: r.Header.Get(protocol.HeaderClient),
		ClientOS:      r.Header.Get(protocol.HeaderOS),
		Hostname:      r.Header.Get(protocol.HeaderHostname),
		RemoteAddr:    clientIP(r),
		Node:          s.cfg.NodeName(),
	})
	if err != nil {
		logf("create agent session: %v", err)
		as.ctrl.Send(protocol.Message{Type: protocol.TypeError, Error: "server error, try again"})
		return
	}
	s.reg.AddSession(as)
	logf("session %s connected: %s from %s (%s)", as.ID, acct.Email, clientIP(r), r.Header.Get(protocol.HeaderOS))
	defer as.close("disconnected")

	// Clients never open additional streams; refuse them.
	go func() {
		for {
			st, err := mux.Accept()
			if err != nil {
				return
			}
			st.Close()
		}
	}()

	if err := as.ctrl.Send(protocol.Message{
		Type:          protocol.TypeWelcome,
		SessionID:     as.ID,
		Account:       acct.Email,
		ServerVersion: Version,
		DashboardURL:  s.cfg.DashboardURL(),
	}); err != nil {
		return
	}

	for {
		m, err := as.ctrl.Recv()
		if err != nil {
			return
		}
		switch m.Type {
		case protocol.TypeBind:
			go as.bind(ctx, m)
		case protocol.TypeUnbind:
			as.unbind(m.ID, "")
		}
	}
}

func acceptTimeout(mux *yamux.Session, d time.Duration) (net.Conn, error) {
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := mux.Accept()
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-time.After(d):
		mux.Close()
		return nil, errors.New("timeout waiting for control stream")
	}
}

type bindError string

func (e bindError) Error() string { return string(e) }

func (as *AgentSession) bind(ctx context.Context, m protocol.Message) {
	t, warning, err := as.createTunnel(ctx, m)
	if err != nil {
		var be bindError
		msg := "internal server error"
		if errors.As(err, &be) {
			msg = string(be)
		} else {
			logf("bind %s for %s: %v", m.ID, as.Account.Email, err)
		}
		as.ctrl.Send(protocol.Message{Type: protocol.TypeBindError, ID: m.ID, Error: msg})
		return
	}
	logf("tunnel %s online: %s -> %s (%s)", t.ID, t.PublicURL, t.LocalAddr, as.Account.Email)
	as.ctrl.Send(protocol.Message{
		Type: protocol.TypeBound, ID: m.ID, TunnelID: t.ID, URL: t.PublicURL,
		AuthMode: t.Policy().Mode, Static: t.Static, Warning: warning, RemotePort: t.RemotePort,
		BrowserWarning: t.warn.Load() && t.Policy().Mode == protocol.AuthNone,
	})
	if t.Proto == protocol.ProtoHTTP {
		as.srv.certs.Prewarm(t.Hostname)
	}
}

func (as *AgentSession) createTunnel(ctx context.Context, m protocol.Message) (*Tunnel, string, error) {
	s := as.srv
	b := m.Bind
	if b == nil || m.ID == "" {
		return nil, "", bindError("malformed bind request")
	}
	proto := b.Proto
	if proto == "" {
		proto = protocol.ProtoHTTP
	}
	var local *url.URL
	switch proto {
	case protocol.ProtoHTTP:
		var err error
		local, err = url.Parse(b.LocalAddr)
		if err != nil || (local.Scheme != "http" && local.Scheme != "https") || local.Host == "" {
			return nil, "", bindError("invalid local address " + b.LocalAddr)
		}
	case protocol.ProtoTCP, protocol.ProtoTLS:
		if strings.TrimSpace(b.LocalAddr) == "" {
			return nil, "", bindError("missing local address")
		}
		if b.Auth != nil && b.Auth.Mode != "" && b.Auth.Mode != protocol.AuthNone {
			return nil, "", bindError("password and OIDC protection work for HTTP tunnels only; use an IP allow list for " + proto + " tunnels")
		}
		if proto == protocol.ProtoTLS && s.cfg.TLSMode == "off" {
			return nil, "", bindError("TLS tunnels are not available: this server does not terminate TLS itself")
		}
	default:
		return nil, "", bindError("unknown tunnel protocol " + proto)
	}
	allow, err := parseAllowList(b.AllowIPs)
	if err != nil {
		return nil, "", err
	}
	as.mu.Lock()
	_, dup := as.tunnels[m.ID]
	as.mu.Unlock()
	if dup {
		return nil, "", bindError("duplicate tunnel id " + m.ID)
	}
	acct := as.Account
	meter, err := s.meterFor(ctx, acct.UserID)
	if err != nil {
		return nil, "", err
	}
	if meter.blocked.Load() {
		meter.mu.Lock()
		msg := quotaMessage(meter.quotaBytes, meter.periodEnd)
		meter.mu.Unlock()
		return nil, "", bindError(msg)
	}
	if max := s.rt().MaxTunnelsPerUser; max > 0 && !acct.IsAdmin && s.reg.CountForUser(acct.UserID) >= max {
		return nil, "", bindError(fmt.Sprintf("tunnel limit reached (%d online tunnels per account)", max))
	}

	t := &Tunnel{
		BindID:    m.ID,
		Name:      b.Name,
		Proto:     proto,
		LocalAddr: b.LocalAddr,
		UserID:    acct.UserID,
		StartedAt: time.Now(),
		allow:     allow,
		meter:     meter,
		session:   as,
	}
	if t.Name == "" {
		t.Name = m.ID
	}
	if local != nil {
		switch b.HostHeader {
		case "", "preserve":
		case "rewrite":
			t.HostHeader = local.Host
		default:
			t.HostHeader = b.HostHeader
		}
	}

	var warning string
	var domain *Domain
	if proto == protocol.ProtoTCP {
		// Same per-account lock as hostnames: concurrent binds must not race
		// for the account's reserved ports.
		unlock := s.lockUser(acct.UserID)
		static, teamID, err := s.claimTCP(ctx, acct, b, t)
		if err == nil {
			if err = s.claim(t, acct); err != nil {
				s.closeTCP(t)
			}
		}
		unlock()
		if err != nil {
			return nil, "", err
		}
		if b.Pin && !static {
			if err := s.pinTCP(ctx, acct, t.RemotePort); err != nil {
				var be bindError
				if !errors.As(err, &be) {
					s.releaseTunnel(t)
					return nil, "", err
				}
				warning = string(be)
			} else {
				static = true
			}
		}
		t.Static, t.TeamID = static, teamID
		t.PublicURL = "tcp://" + t.Hostname
	} else {
		// Choosing a default/remembered hostname and claiming it must not race
		// with the other binds of the same account (tund start --all).
		unlock := s.lockUser(acct.UserID)
		domain, err = s.claimHostname(ctx, acct, b, t)
		unlock()
		if err != nil {
			return nil, "", err
		}
		usable := domain != nil && s.canUseDomain(ctx, domain, acct.UserID)
		if b.Pin && s.certs.underBase(t.Hostname) && !usable {
			d, err := s.pinHostname(ctx, acct, t.Hostname)
			if err != nil {
				var be bindError
				if !errors.As(err, &be) {
					s.releaseTunnel(t)
					return nil, "", err
				}
				warning = string(be)
			} else {
				domain, usable = d, true
			}
		}
		if !usable {
			domain = nil // someone else's static hostname: no policy, not static for us
		}
		t.Static = domain != nil && domain.Kind == "subdomain"
		if domain != nil {
			t.TeamID = domain.TeamID
		}
		t.PublicURL = s.cfg.PublicURL(t.Hostname)
		if proto == protocol.ProtoTLS {
			t.PublicURL = "tls://" + t.Hostname
		}
	}
	host := t.Hostname
	if _, blocked := s.blockedReason(host); blocked && proto != protocol.ProtoTCP {
		s.releaseTunnel(t)
		return nil, "", bindError(host + " is blocked on this server")
	}
	explicitName := b.Hostname != "" || (b.Subdomain != "" && !b.Auto) || b.Pin
	if proto == protocol.ProtoTCP || explicitName || !s.certs.underBase(host) {
		if err := s.checkBindAbuse(acct, proto, host); err != nil {
			s.releaseTunnel(t)
			return nil, "", err
		}
	}

	pol, clientPolicy := Policy{Mode: protocol.AuthNone}, false
	if proto == protocol.ProtoHTTP {
		if pol, clientPolicy, err = s.bindPolicy(ctx, acct.UserID, b.Auth, domain); err != nil {
			s.releaseTunnel(t)
			return nil, "", err
		}
	}
	t.policy, t.clientPolicy = pol, clientPolicy
	t.ownerTrusted.Store(acct.IsAdmin || acct.Trusted)
	s.updateWarn(t)

	row := TunnelRow{
		SessionID: as.ID, UserID: acct.UserID, Name: t.Name, Hostname: host,
		PublicURL: t.PublicURL, LocalAddr: t.LocalAddr, AuthMode: pol.Mode,
		Proto: proto, RemotePort: t.RemotePort, Node: s.cfg.NodeName(),
	}
	t.ID, err = s.store.CreateTunnel(ctx, row)
	var inUse *errInUse
	if errors.As(err, &inUse) && s.takeOver(ctx, acct, inUse) {
		t.ID, err = s.store.CreateTunnel(ctx, row)
	}
	if err != nil {
		s.releaseTunnel(t)
		if errors.As(err, &inUse) {
			return nil, "", bindError(host + " is already in use by another tunnel")
		}
		return nil, "", err
	}
	if proto == protocol.ProtoHTTP {
		s.setupProxy(t)
	}

	as.mu.Lock()
	if as.closed {
		as.mu.Unlock()
		s.releaseTunnel(t)
		s.store.EndTunnel(context.Background(), t.ID)
		return nil, "", bindError("session closed")
	}
	as.tunnels[m.ID] = t
	as.mu.Unlock()
	s.reg.Activate(t)
	if proto == protocol.ProtoTCP {
		go s.serveTCP(t)
	}
	s.store.Notify(ctx, "tund_tunnels", tunnelEvent(t, s.cfg.NodeName(), "online"))
	return t, warning, nil
}

// takeOver asks the node holding a hostname for the same account to give it
// up when that session is dead (a client reconnecting through another node).
func (s *Server) takeOver(ctx context.Context, acct Account, h *errInUse) bool {
	if s.cluster == nil || h.Node == "" || h.Node == s.cluster.name || h.UserID != acct.UserID {
		return false
	}
	n, ok := s.cluster.node(h.Node)
	if !ok {
		return false // the node is dead; CreateTunnel ends its rows on retry soon
	}
	reply, err := s.cluster.command(ctx, n, relayCmd{Cmd: "takeover", TunnelID: h.TunnelID, UserID: acct.UserID})
	return err == nil && reply.OK
}

func tunnelEvent(t *Tunnel, node, event string) map[string]any {
	return map[string]any{"id": t.ID, "user_id": t.UserID, "hostname": t.Hostname, "event": event,
		"node": node, "proto": t.Proto, "remote_port": t.RemotePort}
}

// releaseTunnel undoes a claim: registry entry and, for TCP, the listener.
func (s *Server) releaseTunnel(t *Tunnel) {
	s.reg.Release(t)
	s.closeTCP(t)
}

func (s *Server) closeTCP(t *Tunnel) {
	if t.listener != nil {
		t.listener.Close()
		s.tcp.release(t.RemotePort, t)
	}
}

// pinTCP reserves port for the account (a "static address").
func (s *Server) pinTCP(ctx context.Context, acct Account, port int) error {
	if max := s.rt().MaxPinnedPerUser; max > 0 && !acct.IsAdmin {
		n, err := s.store.CountStatic(ctx, acct.UserID)
		if err != nil {
			return err
		}
		if n >= max {
			return bindError(fmt.Sprintf("not pinned: static address limit reached (%d per account); remove one in the dashboard", max))
		}
	}
	err := s.store.ReserveTCP(ctx, acct.UserID, port)
	if errors.Is(err, errTaken) {
		return bindError(fmt.Sprintf("not pinned: port %d is reserved by another account", port))
	}
	return err
}

// lockUser serializes hostname selection per account.
func (s *Server) lockUser(userID string) func() {
	v, _ := s.userLocks.LoadOrStore(userID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// claimHostname resolves the hostname for a bind (see "Bind resolution" in
// docs/SPEC.md), claims it in the registry and returns the governing domain row.
func (s *Server) claimHostname(ctx context.Context, acct Account, b *protocol.Bind, t *Tunnel) (*Domain, error) {
	explicit := b.Hostname != "" || (b.Subdomain != "" && !b.Auto)
	if b.Random && explicit {
		return nil, bindError("a random hostname cannot be combined with --subdomain or --domain")
	}
	if explicit {
		host, d, err := s.resolveExplicit(ctx, acct.UserID, b)
		if err != nil {
			return nil, err
		}
		t.Hostname = host
		return d, s.claim(t, acct)
	}

	// Opportunistic choices: take the first that is free, otherwise move on.
	if !b.Random {
		d, err := s.store.DefaultStatic(ctx, acct.UserID)
		switch {
		case err == nil:
			// claim (not a plain in-use check) so a reconnecting client takes
			// its URL back from its own dead session.
			t.Hostname = d.Hostname
			if s.claim(t, acct) == nil {
				return d, nil
			}
		case !errors.Is(err, errNotFound):
			return nil, err
		default:
			if d, err := s.autoPin(ctx, acct); err != nil {
				return nil, err
			} else if d != nil {
				t.Hostname = d.Hostname
				if s.claim(t, acct) == nil {
					return d, nil
				}
			}
		}
	}
	if b.Subdomain != "" && b.Auto {
		label := strings.ToLower(strings.TrimSpace(b.Subdomain))
		host := label + "." + s.cfg.BaseDomain
		if validLabel(label) && !s.reservedLabel(label) {
			d, err := s.store.ResolveDomain(ctx, host)
			if errors.Is(err, errNotFound) || (err == nil && s.canUseDomain(ctx, d, acct.UserID)) {
				t.Hostname = host
				if s.claim(t, acct) == nil {
					return d, nil
				}
			}
		}
	}
	for range 20 {
		host, err := s.freeRandomHost(ctx)
		if err != nil {
			return nil, err
		}
		t.Hostname = host
		if s.claim(t, acct) == nil {
			return nil, nil
		}
	}
	return nil, errors.New("could not find a free random subdomain")
}

// claim reserves t.Hostname in the registry, taking it over from a dead
// session of the same account.
func (s *Server) claim(t *Tunnel, acct Account) error {
	cur, ok := s.reg.Claim(t)
	if ok {
		return nil
	}
	if cur.UserID != acct.UserID || !cur.session.stale() {
		return bindError(t.Hostname + " is already in use by another tunnel")
	}
	// Our own client reconnected before the old session timed out.
	cur.session.close("replaced by a new connection")
	if _, ok := s.reg.Claim(t); !ok {
		return bindError(t.Hostname + " is already in use by another tunnel")
	}
	return nil
}

func (s *Server) freeRandomHost(ctx context.Context) (string, error) {
	for range 20 {
		host := randomLabel() + "." + s.cfg.BaseDomain
		if s.reg.InUse(host) {
			continue
		}
		if _, err := s.store.ResolveDomain(ctx, host); errors.Is(err, errNotFound) {
			return host, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not find a free random subdomain")
}

// autoPin gives an account without static hostnames its first one, so that
// `tund http 3000` returns the same URL every time. It returns nil when
// auto-pinning is off or not allowed.
func (s *Server) autoPin(ctx context.Context, acct Account) (*Domain, error) {
	if !s.rt().AutoPin {
		return nil, nil
	}
	n, err := s.store.CountStatic(ctx, acct.UserID)
	if err != nil || n > 0 {
		return nil, err
	}
	host, err := s.freeRandomHost(ctx)
	if err != nil {
		return nil, err
	}
	d, err := s.pinHostname(ctx, acct, host)
	var be bindError
	if errors.As(err, &be) {
		return nil, nil
	}
	if err == nil {
		logf("pinned %s as the default static hostname of %s", host, acct.Email)
	}
	return d, err
}

// pinHostname makes host a static hostname of the account (the default if it
// has none). Limit and ownership problems are returned as bindError.
func (s *Server) pinHostname(ctx context.Context, acct Account, host string) (*Domain, error) {
	if max := s.rt().MaxPinnedPerUser; max > 0 && !acct.IsAdmin {
		n, err := s.store.CountStatic(ctx, acct.UserID)
		if err != nil {
			return nil, err
		}
		if n >= max {
			return nil, bindError(fmt.Sprintf("not pinned: static hostname limit reached (%d per account); remove one in the dashboard", max))
		}
	}
	d, err := s.store.PinStatic(ctx, acct.UserID, host)
	if errors.Is(err, errTaken) {
		return nil, bindError("not pinned: " + host + " is already pinned by another account")
	}
	return d, err
}

// resolveExplicit handles a bind that names its hostname.
func (s *Server) resolveExplicit(ctx context.Context, userID string, b *protocol.Bind) (string, *Domain, error) {
	base := s.cfg.BaseDomain
	var label string
	if b.Hostname != "" {
		host := normalizeHost(b.Hostname)
		if !strings.HasSuffix(host, "."+base) {
			if !validHostname(host) {
				return "", nil, bindError("invalid hostname " + host)
			}
			if ok, err := s.customDomainsAllowed(ctx, userID); err != nil {
				return "", nil, err
			} else if !ok {
				return "", nil, bindError(errCustomDomainsOff)
			}
			d, err := s.store.ResolveDomain(ctx, host)
			if errors.Is(err, errNotFound) || (err == nil && !s.canUseDomain(ctx, d, userID)) {
				return "", nil, bindError(host + " is not one of your or your teams' domains; add and verify it in the dashboard first")
			}
			if err != nil {
				return "", nil, err
			}
			if !d.Verified {
				return "", nil, bindError(d.Hostname + " is not verified yet; finish DNS verification in the dashboard")
			}
			switch d.Approval {
			case "pending":
				return "", nil, bindError(d.Hostname + " is waiting for approval by the administrator of " + s.cfg.DashboardHost)
			case "rejected":
				return "", nil, bindError(d.Hostname + " was rejected by the administrator of " + s.cfg.DashboardHost)
			}
			return host, d, nil
		}
		label = strings.TrimSuffix(host, "."+base)
	} else {
		label = strings.ToLower(strings.TrimSpace(b.Subdomain))
	}

	if !validLabel(label) {
		return "", nil, bindError(fmt.Sprintf("invalid subdomain %q: use a-z, 0-9 and dashes (max 63 characters)", label))
	}
	if s.reservedLabel(label) {
		return "", nil, bindError(fmt.Sprintf("subdomain %q is reserved", label))
	}
	host := label + "." + base
	d, err := s.store.ResolveDomain(ctx, host)
	switch {
	case errors.Is(err, errNotFound):
		return host, nil, nil
	case err != nil:
		return "", nil, err
	case !s.canUseDomain(ctx, d, userID):
		return "", nil, bindError(fmt.Sprintf("subdomain %q is pinned by another account", label))
	}
	return host, d, nil
}

// canUseDomain: personal domains by their owner, team domains by any member.
func (s *Server) canUseDomain(ctx context.Context, d *Domain, userID string) bool {
	if d.TeamID == "" {
		return d.UserID == userID
	}
	ok, err := s.store.IsTeamMember(ctx, d.TeamID, userID)
	if err != nil {
		logf("team membership check: %v", err)
	}
	return ok
}

// resolveProvider finds the OIDC provider a client refers to: "<team>/<slug>"
// or "<slug>" (personal first, then the unique match among the user's teams).
func (s *Server) resolveProvider(ctx context.Context, userID, ref string) (*OIDCProvider, error) {
	ref = strings.TrimSpace(ref)
	if team, slug, ok := strings.Cut(ref, "/"); ok {
		ps, err := s.store.TeamProviders(ctx, userID, team, slug)
		if err != nil {
			return nil, err
		}
		if len(ps) == 0 {
			return nil, bindError(fmt.Sprintf("OIDC provider %q not found: no provider %q in a team %q you belong to", ref, slug, team))
		}
		return ps[0], nil
	}
	p, err := s.store.PersonalProvider(ctx, userID, ref)
	if err == nil || !errors.Is(err, errNotFound) {
		return p, err
	}
	ps, err := s.store.TeamProviders(ctx, userID, "", ref)
	if err != nil {
		return nil, err
	}
	switch len(ps) {
	case 0:
		return nil, bindError(fmt.Sprintf("OIDC provider %q not found; add it in the dashboard under Access (or use <team>/<provider> for a team's provider)", ref))
	case 1:
		return ps[0], nil
	}
	var names []string
	for _, p := range ps {
		names = append(names, p.TeamSlug+"/"+p.Slug)
	}
	return nil, bindError(fmt.Sprintf("OIDC provider %q exists in several of your teams; use one of: %s", ref, strings.Join(names, ", ")))
}

func (s *Server) bindPolicy(ctx context.Context, userID string, a *protocol.Auth, d *Domain) (Policy, bool, error) {
	if a != nil && a.Mode != "" {
		switch a.Mode {
		case protocol.AuthNone:
			return Policy{Mode: protocol.AuthNone}, true, nil
		case protocol.AuthPassword:
			if len(a.Password) < 4 {
				return Policy{}, false, bindError("password must have at least 4 characters")
			}
			h, err := pwhash.Hash(a.Password)
			if err != nil {
				return Policy{}, false, err
			}
			return Policy{Mode: protocol.AuthPassword, PasswordHash: h}, true, nil
		case protocol.AuthOIDC:
			p, err := s.resolveProvider(ctx, userID, a.Provider)
			if err != nil {
				return Policy{}, false, err
			}
			return Policy{Mode: protocol.AuthOIDC, ProviderID: p.ID, Allow: a.Allow}, true, nil
		default:
			return Policy{}, false, bindError("unknown auth mode " + a.Mode)
		}
	}
	return domainPolicy(d), false, nil
}

func domainPolicy(d *Domain) Policy {
	if d == nil {
		return Policy{Mode: protocol.AuthNone}
	}
	switch d.AuthMode {
	case protocol.AuthPassword:
		if d.AuthPasswordHash != "" {
			return Policy{Mode: protocol.AuthPassword, PasswordHash: d.AuthPasswordHash}
		}
	case protocol.AuthOIDC:
		if d.AuthOIDCProviderID != "" {
			return Policy{Mode: protocol.AuthOIDC, ProviderID: d.AuthOIDCProviderID, Allow: d.AuthOIDCAllow}
		}
		// Provider deleted: fail closed rather than exposing the site.
		return Policy{Mode: protocol.AuthOIDC}
	}
	return Policy{Mode: protocol.AuthNone}
}

// stale reports whether the session no longer answers pings.
func (as *AgentSession) stale() bool {
	done := make(chan error, 1)
	go func() {
		_, err := as.mux.Ping()
		done <- err
	}()
	select {
	case err := <-done:
		return err != nil
	case <-time.After(3 * time.Second):
		return true
	}
}

// openStream opens a data stream to the client for the given tunnel.
func (as *AgentSession) openStream(ctx context.Context, bindID string) (net.Conn, error) {
	return as.openStreamFor(ctx, bindID, "")
}

// openStreamFor is openStream with the visitor's address for the client's log.
func (as *AgentSession) openStreamFor(ctx context.Context, bindID, remote string) (net.Conn, error) {
	st, err := as.mux.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("tunnel session closed: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		st.SetDeadline(dl)
	} else {
		st.SetDeadline(time.Now().Add(15 * time.Second))
	}
	if err := protocol.WriteStreamHeader(st, protocol.StreamHeader{Tunnel: bindID, Remote: remote}); err != nil {
		st.Close()
		return nil, err
	}
	if err := protocol.ReadStreamStatus(st); err != nil {
		st.Close()
		return nil, err
	}
	st.SetDeadline(time.Time{})
	return st, nil
}

func (as *AgentSession) unbind(bindID, reason string) {
	as.mu.Lock()
	t := as.tunnels[bindID]
	delete(as.tunnels, bindID)
	as.mu.Unlock()
	if t == nil {
		return
	}
	if reason != "" {
		as.ctrl.Send(protocol.Message{Type: protocol.TypeClosed, ID: bindID, Error: reason})
	}
	as.srv.teardown(t)
}

func (as *AgentSession) close(reason string) {
	as.mu.Lock()
	if as.closed {
		as.mu.Unlock()
		return
	}
	as.closed = true
	tunnels := as.tunnels
	as.tunnels = map[string]*Tunnel{}
	as.mu.Unlock()

	if reason != "disconnected" {
		as.ctrl.Send(protocol.Message{Type: protocol.TypeError, Error: reason})
	}
	for _, t := range tunnels {
		as.srv.teardown(t)
	}
	as.mux.Close()
	as.srv.reg.RemoveSession(as)
	as.srv.store.EndAgentSession(context.Background(), as.ID)
	logf("session %s closed (%s)", as.ID, reason)
}

func (s *Server) teardown(t *Tunnel) {
	s.releaseTunnel(t)
	if t.transport != nil {
		t.transport.CloseIdleConnections()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.store.EndTunnel(ctx, t.ID)
	s.store.Notify(ctx, "tund_tunnels", tunnelEvent(t, s.cfg.NodeName(), "offline"))
	logf("tunnel %s offline: %s", t.ID, t.PublicURL)
}
