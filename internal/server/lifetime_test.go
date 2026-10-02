package server

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestEffectiveLifetime(t *testing.T) {
	zero, ninety := 0, 90
	cases := []struct {
		global   int
		admin    bool
		override *int
		want     time.Duration
	}{
		{0, false, nil, 0},                     // default: unlimited
		{120, false, nil, 2 * time.Hour},       // instance limit
		{120, true, nil, 0},                    // admins are exempt
		{120, false, &zero, 0},                 // unlimited for this account
		{0, false, &ninety, 90 * time.Minute},  // limited for this account only
		{120, true, &ninety, 90 * time.Minute}, // the override applies to admins too
	}
	for _, c := range cases {
		if got := effectiveLifetime(c.global, c.admin, c.override); got != c.want {
			t.Errorf("effectiveLifetime(%d, %v, %v) = %v, want %v", c.global, c.admin, c.override, got, c.want)
		}
	}
}

func TestFormatLifetime(t *testing.T) {
	for d, want := range map[time.Duration]string{
		45 * time.Minute: "45m", 60 * time.Minute: "1h", 90 * time.Minute: "1h30m",
		8 * time.Hour: "8h", 24 * time.Hour: "1d", 72 * time.Hour: "3d", 25 * time.Hour: "25h",
	} {
		if got := formatLifetime(d); got != want {
			t.Errorf("formatLifetime(%v) = %q, want %q", d, got, want)
		}
	}
}

// Runs against a real database: TUND_TEST_DATABASE_URL=postgres://… go test ./internal/server -run LifetimePostgres
func TestTunnelLifetimePostgres(t *testing.T) {
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
	s.runtime.Store(&Runtime{TunnelLifetime: 120})
	var id string
	if err := st.pool.QueryRow(ctx, `insert into users (email, password_hash) values ('lifetime-test@example.com', 'x') returning id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	defer st.pool.Exec(ctx, `delete from users where id = $1`, id)

	if got, err := s.tunnelLifetime(ctx, id); err != nil || got != 2*time.Hour {
		t.Fatalf("instance limit: %v, %v", got, err)
	}
	st.pool.Exec(ctx, `update users set tunnel_lifetime_minutes = 15 where id = $1`, id)
	if got, err := s.tunnelLifetime(ctx, id); err != nil || got != 15*time.Minute {
		t.Fatalf("override: %v, %v", got, err)
	}
}
