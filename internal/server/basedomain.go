package server

import (
	"context"
	"regexp"
	"slices"
	"strings"
)

// Moving tunnels to another base domain (docs/SPEC.md "Changing the base
// domain"): TUND_PREVIOUS_BASE_DOMAINS lists the base domains tunnels used
// before, e.g. tund.io after a move to on.tund.io. On start, static hostnames
// and blocked hosts move to the new base; visitors of an old hostname are
// redirected; binds that still name one get the same label under the new base.

// previousLabel returns the label of host when it is exactly one label under
// a previous base domain ("app" for app.tund.io). Hosts the server serves for
// other reasons never match: the dashboard, redirect hosts, the base domain
// itself and anything under it (a.on.tund.io also ends in .tund.io).
func (c *Config) previousLabel(host string) (string, bool) {
	if host == c.DashboardHost || c.RedirectHosts[host] || host == c.BaseDomain || strings.HasSuffix(host, "."+c.BaseDomain) {
		return "", false
	}
	for _, prev := range c.PreviousBaseDomains {
		if label, ok := strings.CutSuffix(host, "."+prev); ok && validLabel(label) {
			return label, true
		}
	}
	return "", false
}

// movedHost is the hostname under the current base that an old hostname
// redirects to, or "" when host isn't an old tunnel hostname.
func (s *Server) movedHost(host string) string {
	label, ok := s.cfg.previousLabel(host)
	if !ok || s.reservedLabel(label) {
		return ""
	}
	return label + "." + s.cfg.BaseDomain
}

// moveFromPreviousBases runs on every start while TUND_PREVIOUS_BASE_DOMAINS
// is set; once everything moved it finds nothing to do.
func (s *Server) moveFromPreviousBases(ctx context.Context) error {
	if len(s.cfg.PreviousBaseDomains) == 0 {
		return nil
	}
	reserved := make([]string, 0, len(s.reserved))
	for l := range s.reserved {
		reserved = append(reserved, l)
	}
	slices.Sort(reserved)
	moved, blocked, skipped, err := s.store.MoveToBaseDomain(ctx, s.cfg.BaseDomain, s.cfg.PreviousBaseDomains, reserved)
	if err != nil {
		return err
	}
	if moved > 0 || blocked > 0 {
		logf("moved %d static hostnames and %d blocked hosts to %s", moved, blocked, s.cfg.BaseDomain)
	}
	for _, h := range skipped {
		logf("static hostname %s stays where it is: its name under %s is taken or reserved; delete it in the dashboard", h, s.cfg.BaseDomain)
	}
	return nil
}

// MoveToBaseDomain renames static hostnames and blocked hosts that are exactly
// one label under a previous base domain to the same label under base, in one
// transaction. Static hostnames whose new name is already pinned or reserved
// stay as they are and are returned in skipped; a blocked host whose new name
// is already blocked is dropped.
func (s *Store) MoveToBaseDomain(ctx context.Context, base string, previous, reserved []string) (moved, blocked int, skipped []string, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, nil, err
	}
	defer tx.Rollback(ctx)
	// Nodes of a cluster start at the same time.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(727275)`); err != nil {
		return 0, 0, nil, err
	}
	// One previous base at a time: the same label under two of them must not
	// pass the "not taken" check in one statement.
	for _, prev := range previous {
		pattern := `^[a-z0-9-]+\.` + regexp.QuoteMeta(prev) + `$`
		tag, err := tx.Exec(ctx, `
			update domains d set hostname = split_part(d.hostname, '.', 1) || '.' || $2
			where d.kind = 'subdomain' and d.hostname ~ $1
				and split_part(d.hostname, '.', 1) <> all($3::text[])
				and not exists (select 1 from domains x where x.hostname = split_part(d.hostname, '.', 1) || '.' || $2)`,
			pattern, base, reserved)
		if err != nil {
			return 0, 0, nil, err
		}
		moved += int(tag.RowsAffected())
		tag, err = tx.Exec(ctx, `
			with old as (delete from blocked_hosts where hostname ~ $1 returning hostname, reason, created_by, created_at)
			insert into blocked_hosts (hostname, reason, created_by, created_at)
			select split_part(hostname, '.', 1) || '.' || $2, reason, created_by, created_at from old
			on conflict (hostname) do nothing`, pattern, base)
		if err != nil {
			return 0, 0, nil, err
		}
		blocked += int(tag.RowsAffected())
		rows, err := tx.Query(ctx, `select hostname from domains where kind = 'subdomain' and hostname ~ $1 order by hostname`, pattern)
		if err != nil {
			return 0, 0, nil, err
		}
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err != nil {
				rows.Close()
				return 0, 0, nil, err
			}
			skipped = append(skipped, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, 0, nil, err
		}
	}
	return moved, blocked, skipped, tx.Commit(ctx)
}
