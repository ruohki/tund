package server

import (
	"context"
	"os"
	"testing"
)

func TestFeatureEnabled(t *testing.T) {
	on, off := true, false
	cases := []struct {
		global, admin bool
		override      *bool
		want          bool
	}{
		{false, false, nil, false}, // default: off
		{true, false, nil, true},   // turned on for the instance
		{false, true, nil, true},   // admins don't need the setting
		{false, false, &on, true},  // enabled for this account
		{true, false, &off, false}, // disabled for this account
		{false, true, &off, false}, // the override wins for admins too
	}
	for _, c := range cases {
		if got := featureEnabled(c.global, c.admin, c.override); got != c.want {
			t.Errorf("featureEnabled(%v, %v, %v) = %v", c.global, c.admin, c.override, got)
		}
	}
}

// Runs against a real database: TUND_TEST_DATABASE_URL=postgres://… go test ./internal/server -run Passthrough
func TestPassthroughAllowedPostgres(t *testing.T) {
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
	s.runtime.Store(&Runtime{Passthrough: false})

	var id string
	if err := st.pool.QueryRow(ctx, `insert into users (email, password_hash) values ('passthrough-test@example.com', 'x') returning id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	defer st.pool.Exec(ctx, `delete from users where id = $1`, id)

	check := func(want bool, what string) {
		t.Helper()
		got, err := s.passthroughAllowed(ctx, id)
		if err != nil || got != want {
			t.Fatalf("%s: allowed = %v, %v; want %v", what, got, err, want)
		}
	}
	check(false, "default (setting off)")
	s.runtime.Store(&Runtime{Passthrough: true})
	check(true, "setting on")
	st.pool.Exec(ctx, `update users set passthrough = false where id = $1`, id)
	check(false, "override off")
	s.runtime.Store(&Runtime{Passthrough: false})
	st.pool.Exec(ctx, `update users set passthrough = true where id = $1`, id)
	check(true, "override on")
}
