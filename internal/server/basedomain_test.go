package server

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"tund/internal/protocol"
)

func movedConfig() *Config {
	return &Config{
		BaseDomain: "t.tund.example", DashboardHost: "tund.example", PublicScheme: "https", TLSMode: "off",
		RedirectHosts:       map[string]bool{"old-dash.tund.example": true},
		PreviousBaseDomains: []string{"tund.example", "older.example"},
	}
}

func TestPreviousBaseDomainsConfig(t *testing.T) {
	t.Setenv("TUND_DATABASE_URL", "postgres://x")
	t.Setenv("TUND_SECRET", strings.Repeat("s", 16))
	t.Setenv("TUND_INTERNAL_SECRET", strings.Repeat("i", 16))
	t.Setenv("TUND_BASE_DOMAIN", "t.tund.example")
	t.Setenv("TUND_DASHBOARD_HOST", "tund.example")
	t.Setenv("TUND_PREVIOUS_BASE_DOMAINS", " TUND.example., older.example tund.example")
	c, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	// The dashboard's own domain is the usual previous base: tunnels moved off it.
	if !slices.Equal(c.PreviousBaseDomains, []string{"tund.example", "older.example"}) {
		t.Fatalf("PreviousBaseDomains = %q", c.PreviousBaseDomains)
	}
	for _, bad := range []string{"t.tund.example", "x.t.tund.example"} {
		t.Setenv("TUND_PREVIOUS_BASE_DOMAINS", bad)
		if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "TUND_PREVIOUS_BASE_DOMAINS") {
			t.Errorf("%s: err = %v, want a TUND_PREVIOUS_BASE_DOMAINS error", bad, err)
		}
	}
}

func TestPreviousLabel(t *testing.T) {
	c := movedConfig()
	for host, want := range map[string]string{
		"app.tund.example":      "app",
		"app.older.example":     "app",
		"tund.example":          "", // the dashboard
		"old-dash.tund.example": "", // a redirect host
		"t.tund.example":        "", // the base itself
		"app.t.tund.example":    "", // already under the base
		"a.b.tund.example":      "", // not a tunnel hostname of the old base
		"app.other.example":     "",
		"app_x.tund.example":    "",
	} {
		got, ok := c.previousLabel(host)
		if got != want || ok != (want != "") {
			t.Errorf("previousLabel(%q) = %q, %v; want %q", host, got, ok, want)
		}
	}
}

func TestPreviousBaseRedirect(t *testing.T) {
	s, err := New(movedConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.reservedLabel("t") {
		t.Fatal(`"t" must be reserved after a move to t.tund.example`)
	}
	get := func(url string) *discardWriter {
		r, _ := http.NewRequest("POST", url, nil)
		w := &discardWriter{header: http.Header{}}
		s.ServeHTTP(w, r)
		return w
	}
	if w := get("http://Shop.tund.example:443/hook?id=1"); w.code != http.StatusPermanentRedirect || w.header.Get("Location") != "https://shop.t.tund.example/hook?id=1" {
		t.Fatalf("old hostname: got %d %q", w.code, w.header.Get("Location"))
	}
	// Reserved labels were never tunnels: no redirect to a name nobody can bind.
	if w := get("http://www.tund.example/"); w.code == http.StatusPermanentRedirect {
		t.Fatalf("www (reserved) redirected to %q", w.header.Get("Location"))
	}
	if w := get("http://old-dash.tund.example/x"); w.header.Get("Location") != "https://tund.example/x" {
		t.Fatalf("redirect host: got %d %q", w.code, w.header.Get("Location"))
	}
}

func TestDecidePreviousBase(t *testing.T) {
	s := &Server{cfg: movedConfig(), reg: NewRegistry(), reserved: map[string]bool{"t": true, "www": true}}
	blocked := map[string]string{"evil.t.tund.example": "phishing"}
	s.blocked.Store(&blocked)
	later := time.Now().Add(time.Minute)
	s.cluster = &cluster{
		s: s, name: "hel-1",
		nodes: map[string]nodeInfo{"fsn-1": {Name: "fsn-1", LastSeen: time.Now()}},
		owners: map[string]owner{
			"app.t.tund.example": {nodes: []string{"fsn-1"}, proto: protocol.ProtoHTTP, expires: later},
			// Cached misses, so no lookup reaches the (absent) database.
			"app.tund.example": {expires: later}, "evil.tund.example": {expires: later}, "www.tund.example": {expires: later},
		},
	}
	c := &Certs{srv: s}
	ctx := context.Background()
	if err := c.decide(ctx, "app.tund.example"); err != nil {
		t.Fatalf("an old hostname whose tunnel is online under the new base needs a certificate for its redirect: %v", err)
	}
	for _, name := range []string{"evil.tund.example", "www.tund.example"} {
		if err := c.decide(ctx, name); err == nil {
			t.Errorf("%s must not get a certificate", name)
		}
	}
}

func TestWildcardNames(t *testing.T) {
	c := &Certs{srv: &Server{cfg: movedConfig()}}
	if got := c.wildcardNames(); !slices.Equal(got, []string{"*.t.tund.example", "*.tund.example", "*.older.example"}) {
		t.Fatalf("wildcardNames = %q", got)
	}
}

// Runs against a real database: TUND_TEST_DATABASE_URL=postgres://… go test ./internal/server -run PreviousBase
func TestMoveToBaseDomainPreviousBasePostgres(t *testing.T) {
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
	var uid string
	if err := st.pool.QueryRow(ctx, `insert into users (email, password_hash) values ('move-base-test@example.com', 'x') returning id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	defer st.pool.Exec(ctx, `delete from users where id = $1`, uid)
	hosts := []string{"pinned.mv.example", "taken.mv.example", "taken.t.mv.example", "t.mv.example", "a.b.mv.example", "c.mv.example"}
	defer st.pool.Exec(ctx, `delete from blocked_hosts where hostname like '%mv.example'`)
	for _, h := range hosts {
		kind := "subdomain"
		if h == "c.mv.example" {
			kind = "custom" // a verified custom domain stays what it is
		}
		if _, err := st.pool.Exec(ctx, `insert into domains (user_id, hostname, kind, verified_at) values ($1, $2, $3, now())`, uid, h, kind); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range []string{"evil.mv.example", "dup.mv.example", "dup.t.mv.example"} {
		if _, err := st.pool.Exec(ctx, `insert into blocked_hosts (hostname, reason) values ($1, 'test')`, h); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &Config{BaseDomain: "t.mv.example", DashboardHost: "mv.example", PublicScheme: "https", TLSMode: "off", PreviousBaseDomains: []string{"mv.example"}}
	s, err := New(cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	for run := range 2 {
		if err := s.moveFromPreviousBases(ctx); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	moved, blockedN, skipped, err := st.MoveToBaseDomain(ctx, cfg.BaseDomain, cfg.PreviousBaseDomains, []string{"t"})
	if err != nil || moved != 0 || blockedN != 0 {
		t.Fatalf("third run: moved %d, blocked %d, %v; want nothing left to do", moved, blockedN, err)
	}
	if !slices.Equal(skipped, []string{"t.mv.example", "taken.mv.example"}) {
		t.Fatalf("skipped = %q", skipped)
	}

	rows, _ := st.pool.Query(ctx, `select hostname from domains where user_id = $1 order by hostname`, uid)
	var got []string
	for rows.Next() {
		var h string
		rows.Scan(&h)
		got = append(got, h)
	}
	rows.Close()
	want := []string{"a.b.mv.example", "c.mv.example", "pinned.t.mv.example", "t.mv.example", "taken.mv.example", "taken.t.mv.example"}
	if !slices.Equal(got, want) {
		t.Fatalf("domains = %q, want %q", got, want)
	}
	blocked, err := st.BlockedHosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for h, want := range map[string]bool{"evil.t.mv.example": true, "dup.t.mv.example": true, "evil.mv.example": false, "dup.mv.example": false} {
		if _, ok := blocked[h]; ok != want {
			t.Errorf("blocked %s = %v, want %v", h, ok, want)
		}
	}

	// Old names in binds and TLS: the label under the new base, never the leftover.
	if err := s.loadBlocked(ctx); err != nil {
		t.Fatal(err)
	}
	host, _, err := s.resolveExplicit(ctx, uid, &protocol.Bind{Hostname: "pinned.mv.example"})
	if err != nil || host != "pinned.t.mv.example" {
		t.Fatalf("bind pinned.mv.example = %q, %v; want pinned.t.mv.example", host, err)
	}
	if _, _, err := s.resolveExplicit(ctx, uid, &protocol.Bind{Hostname: "t.mv.example"}); err == nil {
		t.Fatal("the leftover t.mv.example (the new base) must not bind as a custom domain")
	}
	c := s.certs
	if err := c.decide(ctx, "pinned.mv.example"); err != nil {
		t.Errorf("pinned.mv.example redirects to a pinned name and needs a certificate: %v", err)
	}
	for _, name := range []string{"unknown.mv.example", "evil.mv.example", "t.mv.example"} {
		if err := c.decide(ctx, name); err == nil {
			t.Errorf("%s must not get a certificate", name)
		}
	}
}
