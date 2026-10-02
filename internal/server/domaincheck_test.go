package server

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
)

func TestCheckOutcome(t *testing.T) {
	const host, token = "*.dev.example.com", "abc123"
	if got := challengeName(host); got != "_tund-challenge.dev.example.com" {
		t.Fatalf("challengeName = %q", got)
	}
	cases := []struct {
		name         string
		values       []string
		err          error
		ok, unsure   bool
		reasonSubstr string
	}{
		{"present", []string{"v=spf1", "tund-verify=abc123"}, nil, true, false, ""},
		{"changed", []string{"tund-verify=someone-else"}, nil, false, false, "no longer contains"},
		{"gone", nil, &net.DNSError{Err: "no such host", IsNotFound: true}, false, false, "is gone"},
		{"timeout", nil, &net.DNSError{Err: "i/o timeout", IsTimeout: true}, false, true, ""},
		{"other error", nil, errors.New("connection refused"), false, true, ""},
	}
	for _, c := range cases {
		ok, unsure, reason := checkOutcome(host, token, c.values, c.err)
		if ok != c.ok || unsure != c.unsure || (c.reasonSubstr != "" && !strings.Contains(reason, c.reasonSubstr)) {
			t.Errorf("%s: ok=%v unsure=%v reason=%q", c.name, ok, unsure, reason)
		}
	}
}

// Runs against a real database: TUND_TEST_DATABASE_URL=postgres://… go test ./internal/server -run DomainMonitoring
func TestDomainMonitoringPostgres(t *testing.T) {
	dsn := os.Getenv("TUND_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TUND_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := OpenStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st}

	var uid, did string
	if err := st.pool.QueryRow(ctx, `insert into users (email, password_hash) values ('domaincheck-test@example.com', 'x') returning id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	defer st.pool.Exec(ctx, `delete from users where id = $1`, uid)
	if err := st.pool.QueryRow(ctx, `insert into domains (user_id, hostname, kind, verified_at, verification_token, approval)
		values ($1, 'monitored.example.com', 'custom', now(), 'tok', 'approved') returning id`, uid).Scan(&did); err != nil {
		t.Fatal(err)
	}

	claim := func() *domainCheck {
		t.Helper()
		due, err := st.ClaimDomainChecks(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range due {
			if d.ID == did {
				return &d
			}
		}
		return nil
	}
	gone := &net.DNSError{Err: "no such host", IsNotFound: true}

	// Never checked: due right away, and claimed only once.
	d := claim()
	if d == nil || d.Token != "tok" {
		t.Fatalf("first claim: %+v", d)
	}
	if again := claim(); again != nil {
		t.Fatal("claimed twice without waiting for the interval")
	}
	s.recordDomainCheck(ctx, *d, []string{"tund-verify=tok"}, nil)

	// Two failures are counted, the third withdraws the verification.
	for i := 1; i <= domainFailLimit; i++ {
		st.pool.Exec(ctx, `update domains set checked_at = now() - interval '1 day' where id = $1`, did)
		d = claim()
		if d == nil || d.Failures != i-1 {
			t.Fatalf("check %d: %+v", i, d)
		}
		s.recordDomainCheck(ctx, *d, nil, gone)
	}
	var verified bool
	var reason string
	if err := st.pool.QueryRow(ctx, `select verified_at is not null, unverified_reason from domains where id = $1`, did).Scan(&verified, &reason); err != nil {
		t.Fatal(err)
	}
	if verified || !strings.Contains(reason, "is gone") {
		t.Fatalf("after %d failures: verified=%v reason=%q", domainFailLimit, verified, reason)
	}
	var audited int
	st.pool.QueryRow(ctx, `select count(*) from audit_log where action = 'domain.unverified' and target = 'monitored.example.com'`).Scan(&audited)
	if audited != 1 {
		t.Fatalf("audit entries: %d", audited)
	}
	st.pool.Exec(ctx, `delete from audit_log where target = 'monitored.example.com'`)

	// Unverified domains are no longer checked.
	st.pool.Exec(ctx, `update domains set checked_at = now() - interval '1 day' where id = $1`, did)
	if d := claim(); d != nil {
		t.Fatal("unverified domain claimed")
	}
}
