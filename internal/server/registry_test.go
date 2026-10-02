package server

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestRegistryPools(t *testing.T) {
	r := NewRegistry()
	mk := func(id, user string, pool bool, key string) *Tunnel {
		return &Tunnel{ID: id, Hostname: "app.example.com", UserID: user, pool: pool, poolKey: key}
	}
	a := mk("a", "u1", true, "k")
	if _, ok := r.Claim(a); !ok {
		t.Fatal("first claim failed")
	}
	r.Activate(a)

	for _, other := range []*Tunnel{mk("x", "u1", false, ""), mk("y", "u1", true, "other"), mk("z", "u2", true, "k")} {
		if cur, ok := r.Claim(other); ok || cur != a {
			t.Fatalf("%s joined the pool", other.ID)
		}
	}
	b, c := mk("b", "u1", true, "k"), mk("c", "u1", true, "k")
	if _, ok := r.Claim(b); !ok {
		t.Fatal("b could not join")
	}
	if _, ok := r.Claim(c); !ok {
		t.Fatal("c could not join while b is pending")
	}
	if got := r.Members("app.example.com"); len(got) != 1 {
		t.Fatalf("pending members routable: %d", len(got))
	}
	r.Activate(b)
	r.Activate(c)
	seen := map[string]int{}
	for range 9 {
		seen[r.Lookup("app.example.com").ID]++
	}
	if seen["a"] != 3 || seen["b"] != 3 || seen["c"] != 3 {
		t.Fatalf("round robin = %v", seen)
	}
	if n := r.CountForUser("u1"); n != 3 {
		t.Fatalf("CountForUser = %d", n)
	}
	if len(r.Tunnels()) != 3 || r.ByID("b") != b {
		t.Fatal("Tunnels/ByID")
	}

	r.Release(b)
	for range 4 {
		if r.Lookup("app.example.com") == b {
			t.Fatal("released member still routed")
		}
	}
	r.Release(a)
	r.Release(c)
	if r.InUse("app.example.com") || r.Lookup("app.example.com") != nil {
		t.Fatal("hostname still in use")
	}
	// A single tunnel blocks pool members.
	s := mk("s", "u1", false, "")
	r.Claim(s)
	if _, ok := r.Claim(mk("p", "u1", true, "k")); ok {
		t.Fatal("pool member joined a single tunnel")
	}
}

func TestPasswordTagFingerprint(t *testing.T) {
	a := Policy{Mode: "password", PasswordHash: "salt1$hash", PasswordTag: "tag"}
	b := Policy{Mode: "password", PasswordHash: "salt2$hash", PasswordTag: "tag"}
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("same password tag, different fingerprints")
	}
	c := Policy{Mode: "password", PasswordHash: "salt1$hash"}
	d := Policy{Mode: "password", PasswordHash: "salt2$hash"}
	if c.Fingerprint() == d.Fingerprint() {
		t.Fatal("stored hashes must still tell policies apart")
	}
}

// Runs against a real database: TUND_TEST_DATABASE_URL=postgres://… go test ./internal/server -run PoolsPostgres
func TestTunnelPoolsPostgres(t *testing.T) {
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
	var u1, u2, sess1, sess2 string
	for _, x := range []struct {
		email string
		id    *string
	}{{"pool-test-1@example.com", &u1}, {"pool-test-2@example.com", &u2}} {
		if err := st.pool.QueryRow(ctx, `insert into users (email, password_hash) values ($1, 'x') returning id`, x.email).Scan(x.id); err != nil {
			t.Fatal(err)
		}
		defer st.pool.Exec(ctx, `delete from users where id = $1`, *x.id)
	}
	if sess1, err = st.CreateAgentSession(ctx, AgentSessionRow{UserID: u1, Node: "node-a"}); err != nil {
		t.Fatal(err)
	}
	if sess2, err = st.CreateAgentSession(ctx, AgentSessionRow{UserID: u2, Node: "node-a"}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"node-a", "node-b"} {
		st.pool.Exec(ctx, `insert into nodes (name, role, relay_url, last_seen) values ($1, 'control', '', now())
			on conflict (name) do update set last_seen = now()`, n)
		defer st.pool.Exec(ctx, `delete from nodes where name = $1`, n)
	}
	const host = "pool-test.example.com"
	row := func(user, sess, node, key string) TunnelRow {
		return TunnelRow{SessionID: sess, UserID: user, Name: "web", Hostname: host, PublicURL: "https://" + host,
			LocalAddr: "http://localhost:3000", AuthMode: "none", Proto: "http", Node: node, PoolKey: key}
	}
	first, err := st.CreateTunnel(ctx, row(u1, sess1, "node-a", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	// Same pool on another node and on the same node: both join.
	if _, err := st.CreateTunnel(ctx, row(u1, sess1, "node-b", "k1")); err != nil {
		t.Fatalf("member on node-b: %v", err)
	}
	if _, err := st.CreateTunnel(ctx, row(u1, sess1, "node-a", "k1")); err != nil {
		t.Fatalf("second member on node-a: %v", err)
	}
	if n, _ := st.CountOnline(ctx, host); n != 3 {
		t.Fatalf("online members = %d", n)
	}
	var stillOnline bool
	st.pool.QueryRow(ctx, `select ended_at is null from tunnels where id = $1`, first).Scan(&stillOnline)
	if !stillOnline {
		t.Fatal("joining ended the first member on the same node")
	}
	nodes, _, err := st.TunnelNodes(ctx, host)
	if err != nil || len(nodes) != 2 {
		t.Fatalf("TunnelNodes = %v, %v", nodes, err)
	}
	// Other settings, a single tunnel or another account: refused.
	for _, r := range []TunnelRow{row(u1, sess1, "node-b", "k2"), row(u1, sess1, "node-b", ""), row(u2, sess2, "node-b", "k1")} {
		var inUse *errInUse
		if _, err := st.CreateTunnel(ctx, r); err == nil || !errors.As(err, &inUse) || !inUse.Pooled {
			t.Fatalf("%+v: %v", r.PoolKey, err)
		}
	}
	st.pool.Exec(ctx, `update tunnels set ended_at = now() where hostname = $1`, host)
}
