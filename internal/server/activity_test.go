package server

import (
	"context"
	"os"
	"testing"
	"time"
)

// Runs against a real database: TUND_TEST_DATABASE_URL=postgres://… go test ./internal/server -run ActivityPostgres
func TestActivityPostgres(t *testing.T) {
	dsn := os.Getenv("TUND_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TUND_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := OpenStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var uid string
	if err := st.pool.QueryRow(ctx, `insert into users (email, password_hash, last_seen_at) values ('activity@example.com', 'x', now() - interval '1 year') returning id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.pool.Exec(ctx, `delete from users where id = $1`, uid) })
	if _, err := st.pool.Exec(ctx, `insert into authtokens (user_id, name, token_hash, token_prefix) values ($1, 'test', $2, 'tund_test')`, uid, sha256Hex("activity-token")); err != nil {
		t.Fatal(err)
	}
	lastSeen := func() time.Duration {
		t.Helper()
		var age time.Duration
		if err := st.pool.QueryRow(ctx, `select extract(epoch from now() - last_seen_at)::bigint * 1000000000 from users where id = $1`, uid).Scan(&age); err != nil {
			t.Fatal(err)
		}
		return age
	}

	acct, err := st.AuthenticateToken(ctx, "activity-token")
	if err != nil {
		t.Fatal(err)
	}
	if acct.UserID != uid || acct.Email != "activity@example.com" || acct.Disabled || acct.EmailVerified {
		t.Errorf("account = %+v", acct)
	}
	if age := lastSeen(); age > time.Minute {
		t.Errorf("token use left last_seen_at %v old", age)
	}

	// Within the hour, token use doesn't write again.
	st.pool.Exec(ctx, `update users set last_seen_at = now() - interval '30 minutes' where id = $1`, uid)
	if _, err := st.AuthenticateToken(ctx, "activity-token"); err != nil {
		t.Fatal(err)
	}
	if age := lastSeen(); age < 29*time.Minute {
		t.Errorf("token use within the hour rewrote last_seen_at (%v old)", age)
	}

	if _, err := st.AuthenticateToken(ctx, "wrong-token"); err != errNotFound {
		t.Errorf("unknown token: err = %v", err)
	}

	st.pool.Exec(ctx, `update users set last_seen_at = now() - interval '1 year' where id = $1`, uid)
	sid, err := st.CreateAgentSession(ctx, AgentSessionRow{UserID: uid})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EndAgentSession(ctx, sid); err != nil {
		t.Fatal(err)
	}
	if age := lastSeen(); age > time.Minute {
		t.Errorf("disconnect left last_seen_at %v old", age)
	}
}
