package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Verified custom domains keep proving ownership (docs/SPEC.md "Custom
// domains"): every node re-checks the domains that are due, claimed with
// FOR UPDATE SKIP LOCKED so each check runs once in the cluster. When the
// _tund-challenge TXT record is gone or no longer matches for
// domainFailLimit checks in a row (about 20 minutes), the domain loses its
// verification: its tunnels end, no certificates are issued for it, and the
// owner is emailed (NOTIFY tund_domain, sent by the dashboard). DNS errors
// (timeouts, SERVFAIL) don't count either way.

const (
	domainCheckLoop    = time.Minute
	domainCheckEvery   = time.Hour        // verified domains that passed
	domainRecheckAfter = 10 * time.Minute // after a failed check
	domainFailLimit    = 3
	domainCheckBatch   = 20
)

// publicResolvers answer the checks, like the dashboard's verification, so a
// node's local resolver or cache can't hide a removed record.
var publicResolvers = []string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:53"}

type domainCheck struct {
	ID, Hostname, Token string
	Failures            int
}

// challengeName is the TXT name that proves ownership of a (wildcard) domain.
func challengeName(hostname string) string {
	return "_tund-challenge." + strings.TrimPrefix(hostname, "*.")
}

// checkOutcome judges one lookup: ok, or a failure with a reason, or
// inconclusive (a DNS error that says nothing about the record).
func checkOutcome(hostname, token string, values []string, err error) (ok, inconclusive bool, reason string) {
	name := challengeName(hostname)
	var dnsErr *net.DNSError
	switch {
	case err != nil && errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return false, false, "the TXT record " + name + " is gone"
	case err != nil:
		return false, true, ""
	}
	want := "tund-verify=" + token
	for _, v := range values {
		if v == want {
			return true, false, ""
		}
	}
	return false, false, "the TXT record " + name + " no longer contains this domain's verification value"
}

func lookupChallenge(ctx context.Context, hostname string) ([]string, error) {
	var mu sync.Mutex
	next := 0
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			mu.Lock()
			addr := publicResolvers[next%len(publicResolvers)]
			next++
			mu.Unlock()
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return r.LookupTXT(ctx, challengeName(hostname))
}

func (s *Server) domainCheckLoop(ctx context.Context) {
	t := time.NewTicker(domainCheckLoop)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkDomains(ctx)
		}
	}
}

func (s *Server) checkDomains(ctx context.Context) {
	due, err := s.store.ClaimDomainChecks(ctx, domainCheckBatch)
	if err != nil {
		if ctx.Err() == nil {
			logf("domain checks: %v", err)
		}
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for _, d := range due {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			values, err := lookupChallenge(ctx, d.Hostname)
			s.recordDomainCheck(ctx, d, values, err)
		}()
	}
	wg.Wait()
}

func (s *Server) recordDomainCheck(ctx context.Context, d domainCheck, values []string, lookupErr error) {
	ok, inconclusive, reason := checkOutcome(d.Hostname, d.Token, values, lookupErr)
	switch {
	case inconclusive:
		logf("domain check %s: %v (not counted)", d.Hostname, lookupErr)
	case ok:
		if err := s.store.DomainCheckPassed(ctx, d.ID); err != nil {
			logf("domain check %s: %v", d.Hostname, err)
		}
	case d.Failures+1 < domainFailLimit:
		logf("domain check %s: %s (%d of %d)", d.Hostname, reason, d.Failures+1, domainFailLimit)
		if err := s.store.DomainCheckFailed(ctx, d.ID); err != nil {
			logf("domain check %s: %v", d.Hostname, err)
		}
	default:
		withdrawn, err := s.store.WithdrawDomain(ctx, d.ID, reason)
		if err != nil {
			logf("domain check %s: %v", d.Hostname, err)
			return
		}
		if withdrawn {
			logf("domain %s lost its verification: %s", d.Hostname, reason)
			s.store.Notify(ctx, "tund_config", map[string]string{"kind": "domain", "id": d.ID})
			s.store.Notify(ctx, "tund_domain", map[string]string{"id": d.ID})
		}
	}
}

func pgInterval(d time.Duration) string { return fmt.Sprintf("%d seconds", int(d.Seconds())) }

// ClaimDomainChecks marks up to limit due domains as being checked and returns them.
func (s *Store) ClaimDomainChecks(ctx context.Context, limit int) ([]domainCheck, error) {
	rows, err := s.pool.Query(ctx, `
		update domains set checked_at = now()
		where id in (
			select id from domains
			where kind = 'custom' and verified_at is not null
				and (checked_at is null
					or (check_failures = 0 and checked_at < now() - $2::interval)
					or (check_failures > 0 and checked_at < now() - $3::interval))
			order by checked_at nulls first
			limit $1
			for update skip locked)
		returning id::text, hostname, verification_token, check_failures`,
		limit, pgInterval(domainCheckEvery), pgInterval(domainRecheckAfter))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domainCheck
	for rows.Next() {
		var d domainCheck
		if err := rows.Scan(&d.ID, &d.Hostname, &d.Token, &d.Failures); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DomainCheckPassed(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `update domains set check_failures = 0 where id = $1`, id)
	return err
}

func (s *Store) DomainCheckFailed(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `update domains set check_failures = check_failures + 1 where id = $1`, id)
	return err
}

// WithdrawDomain takes a domain's verification away and records why in the
// domain and the audit log. It reports false when the domain was already
// unverified (another node, or the owner removed it meanwhile).
func (s *Store) WithdrawDomain(ctx context.Context, id, reason string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var hostname string
	err = tx.QueryRow(ctx, `
		update domains set verified_at = null, unverified_at = now(), unverified_reason = $2,
			unverified_notified_at = null, check_failures = 0
		where id = $1 and verified_at is not null
		returning hostname`, id, reason).Scan(&hostname)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `insert into audit_log (actor_email, action, target, details) values ('system', 'domain.unverified', $1, jsonb_build_object('reason', $2::text))`,
		hostname, reason); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
