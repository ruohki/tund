package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"tund/internal/pwhash"
)

func TestEmailAllowed(t *testing.T) {
	p := Policy{Allow: []string{"alice@example.com", "@corp.io", "partner.org"}}
	cases := map[string]bool{
		"alice@example.com": true,
		"ALICE@example.com": true,
		"bob@example.com":   false,
		"x@corp.io":         true,
		"x@evilcorp.io":     false,
		"y@partner.org":     true,
		"y@notpartner.org":  false,
		"":                  false,
	}
	for email, want := range cases {
		if got := p.EmailAllowed(email); got != want {
			t.Errorf("EmailAllowed(%q) = %v, want %v", email, got, want)
		}
	}
	if !(Policy{}).EmailAllowed("anyone@anywhere") {
		t.Error("empty allow list should admit everyone")
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "/",
		"/a?b=c":            "/a?b=c",
		"//evil.com":        "/",
		"/\\evil.com":       "/",
		"https://evil.com/": "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripAuthCookie(t *testing.T) {
	h := http.Header{}
	h.Add("Cookie", "a=1; _tund_auth=secret; b=2; _tund_warned=x")
	h.Add("Cookie", "_tund_auth=x")
	stripAuthCookie(h)
	if got := h.Values("Cookie"); len(got) != 1 || got[0] != "a=1; b=2" {
		t.Fatalf("got %q", got)
	}
}

func TestSigner(t *testing.T) {
	s := signer{key: []byte("k")}
	tok := s.Sign("auth", authClaims{Host: "a.b", Expires: 5})
	var c authClaims
	if err := s.Verify("auth", tok, &c); err != nil || c.Host != "a.b" {
		t.Fatalf("verify: %v %+v", err, c)
	}
	if err := s.Verify("handoff", tok, &c); err == nil {
		t.Fatal("token must not verify for another purpose")
	}
	if err := (signer{key: []byte("other")}).Verify("auth", tok, &c); err == nil {
		t.Fatal("token must not verify with another key")
	}
}

func TestLabels(t *testing.T) {
	for l, want := range map[string]bool{"abc": true, "a-b-1": true, "-a": false, "a-": false, "A": false, "a.b": false, "": false} {
		if validLabel(l) != want {
			t.Errorf("validLabel(%q) != %v", l, want)
		}
	}
	if l := randomLabel(); !validLabel(l) {
		t.Errorf("random label %q invalid", l)
	}
}

func TestEffectivePolicyTeamSSO(t *testing.T) {
	team := &TeamSSO{Slug: "acme", Required: true, ProviderID: "team-idp", Allow: []string{"@acme.com"}}
	client := &Policy{Mode: "password", PasswordTag: "x"}
	teamDomain := &Domain{TeamID: "t1", AuthMode: "none"}

	cases := []struct {
		name     string
		d        *Domain
		team     *TeamSSO
		client   *Policy
		want     Policy
		enforced bool
	}{
		{"team requires, no flags", teamDomain, team, nil, Policy{Mode: "oidc", ProviderID: "team-idp", Allow: []string{"@acme.com"}}, true},
		{"team requires, flags ignored", teamDomain, team, client, Policy{Mode: "oidc", ProviderID: "team-idp", Allow: []string{"@acme.com"}}, true},
		{"domain's own sso refines the team's", &Domain{TeamID: "t1", AuthMode: "oidc", AuthOIDCProviderID: "other", AuthOIDCAllow: []string{"a@b.com"}}, team, client,
			Policy{Mode: "oidc", ProviderID: "other", Allow: []string{"a@b.com"}}, true},
		{"domain password doesn't weaken it", &Domain{TeamID: "t1", AuthMode: "password", AuthPasswordHash: "h"}, team, nil,
			Policy{Mode: "oidc", ProviderID: "team-idp", Allow: []string{"@acme.com"}}, true},
		{"provider deleted fails closed", teamDomain, &TeamSSO{Required: true}, nil, Policy{Mode: "oidc"}, true},
		{"team requires nothing", teamDomain, &TeamSSO{ProviderID: "team-idp"}, client, *client, false},
		{"personal hostname", nil, nil, nil, Policy{Mode: "none"}, false},
		{"personal hostname with flags", nil, nil, client, *client, false},
	}
	for _, c := range cases {
		got, enforced := effectivePolicy(c.d, c.team, c.client)
		if got.Fingerprint() != c.want.Fingerprint() || enforced != c.enforced {
			t.Errorf("%s: got %+v (enforced %v), want %+v (enforced %v)", c.name, got, enforced, c.want, c.enforced)
		}
	}
}

func TestPolicyFingerprintAndPassword(t *testing.T) {
	h, err := pwhash.Hash("hunter22")
	if err != nil {
		t.Fatal(err)
	}
	tun := &Tunnel{}
	tun.setPolicy(Policy{Mode: "password", PasswordHash: h})
	if !tun.checkPassword("hunter22") || !tun.checkPassword("hunter22") || tun.checkPassword("nope") {
		t.Fatal("password check")
	}
	a := Policy{Mode: "oidc", ProviderID: "p", Allow: []string{"@x"}}
	b := Policy{Mode: "oidc", ProviderID: "p", Allow: []string{"@y"}}
	if a.Fingerprint() == b.Fingerprint() {
		t.Fatal("fingerprint must change with the allow list")
	}
}

func TestUserCode(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		c := newUserCode()
		if len(c) != 9 || c[4] != '-' {
			t.Fatalf("bad user code %q", c)
		}
		for i, r := range c {
			if i != 4 && !strings.ContainsRune(userCodeAlphabet, r) {
				t.Fatalf("user code %q has %q outside the alphabet", c, r)
			}
		}
		seen[c] = true
	}
	if len(seen) < 195 {
		t.Fatalf("user codes repeat too often: %d unique of 200", len(seen))
	}
}

func TestVisitorCookieTossing(t *testing.T) {
	s := &Server{signer: signer{key: []byte("k")}}
	tun := &Tunnel{Hostname: "a.example.com"}
	tun.setPolicy(Policy{Mode: "password", PasswordHash: "x"})
	good := s.signer.Sign("auth", authClaims{Host: "a.example.com", Fingerprint: tun.Policy().Fingerprint(), Expires: 1 << 40})
	planted := s.signer.Sign("auth", authClaims{Host: "evil.example.com", Fingerprint: "f", Expires: 1 << 40})
	r, _ := http.NewRequest("GET", "http://a.example.com/", nil)
	r.Header.Add("Cookie", authCookieName+"="+planted)
	r.Header.Add("Cookie", authCookieName+"="+good)
	if _, ok := s.visitorAuthorized(r, tun, tun.Policy()); !ok {
		t.Fatal("a planted cookie must not hide the valid one")
	}
	r2, _ := http.NewRequest("GET", "http://a.example.com/", nil)
	r2.Header.Add("Cookie", authCookieName+"="+planted)
	if _, ok := s.visitorAuthorized(r2, tun, tun.Policy()); ok {
		t.Fatal("cookie for another host must not authorize")
	}
}

func TestRedirectHosts(t *testing.T) {
	s := &Server{cfg: &Config{BaseDomain: "new.example", DashboardHost: "new.example", PublicScheme: "https", RedirectHosts: map[string]bool{"old.example": true}}}
	r, _ := http.NewRequest("GET", "http://old.example/inspect?id=1", nil)
	w := &discardWriter{header: http.Header{}}
	s.ServeHTTP(w, r)
	if w.code != http.StatusPermanentRedirect || w.header.Get("Location") != "https://new.example/inspect?id=1" {
		t.Fatalf("got %d %q", w.code, w.header.Get("Location"))
	}
	// Edge endpoints are served in place for clients still configured with the old host.
	r2, _ := http.NewRequest("GET", "http://old.example/_tund/health", nil)
	w2 := &discardWriter{header: http.Header{}}
	s.ServeHTTP(w2, r2)
	if w2.code != http.StatusOK {
		t.Fatalf("/_tund/health on old host: got %d", w2.code)
	}
}

func TestAPIBodyDecoding(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(`{"hello":"world"}`))
	zw.Close()
	h := http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}}
	b := apiBody(gz.Bytes(), int64(gz.Len()), false, h)
	if !b.Decoded || b.Text != `{"hello":"world"}` || b.Base64 != "" {
		t.Fatalf("gzip json: %+v", b)
	}
	bin := apiBody([]byte{0x89, 'P', 'N', 'G', 0, 1, 2}, 7, false, http.Header{"Content-Type": {"image/png"}})
	if bin.Text != "" || bin.Base64 == "" {
		t.Fatalf("binary: %+v", bin)
	}
	// A capture cut inside a multi-byte character is still text.
	cut := apiBody([]byte("héllo wörld é")[:14], 100, true, http.Header{"Content-Type": {"text/plain"}})
	if cut.Text == "" {
		t.Fatalf("truncated utf-8: %+v", cut)
	}
}

func TestBrowserWarning(t *testing.T) {
	s := &Server{signer: signer{key: []byte("k")}, cfg: &Config{PublicScheme: "https"}}
	tun := &Tunnel{Hostname: "a.example.com"}
	tun.setPolicy(Policy{Mode: "none"})
	tun.warn.Store(true)
	browser := func() *http.Request {
		r, _ := http.NewRequest("GET", "https://a.example.com/page", nil)
		r.Header.Set("Accept", "text/html,application/xhtml+xml")
		r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh)")
		return r
	}
	if !s.needsWarning(browser(), tun) {
		t.Fatal("a browser navigation must see the warning")
	}
	for name, mod := range map[string]func(*http.Request){
		"curl":        func(r *http.Request) { r.Header.Set("User-Agent", "curl/8") },
		"json":        func(r *http.Request) { r.Header.Set("Accept", "application/json") },
		"post":        func(r *http.Request) { r.Method = "POST" },
		"fetch":       func(r *http.Request) { r.Header.Set("Sec-Fetch-Dest", "empty") },
		"skip header": func(r *http.Request) { r.Header.Set("Tund-Skip-Browser-Warning", "1") },
		"edge path":   func(r *http.Request) { r.URL.Path = "/_tund/warning/accept" },
	} {
		r := browser()
		mod(r)
		if s.needsWarning(r, tun) {
			t.Errorf("%s must not see the warning", name)
		}
	}
	// Accepting sets a host-bound cookie; a cookie planted for another host does not count.
	w := &discardWriter{header: http.Header{}}
	acc, _ := http.NewRequest("POST", "https://a.example.com/_tund/warning/accept", strings.NewReader("next=/page"))
	acc.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.acceptWarning(w, acc, tun)
	cookie := w.header.Get("Set-Cookie")
	if w.code != http.StatusSeeOther || !strings.HasPrefix(cookie, warnCookieName+"=") {
		t.Fatalf("accept: %d %q", w.code, cookie)
	}
	r := browser()
	r.Header.Set("Cookie", strings.SplitN(cookie, ";", 2)[0])
	if s.needsWarning(r, tun) {
		t.Fatal("accepted visitor must not see the warning again")
	}
	other := &Tunnel{Hostname: "b.example.com"}
	other.setPolicy(Policy{Mode: "none"})
	other.warn.Store(true)
	r2 := browser()
	r2.Header.Set("Cookie", strings.SplitN(cookie, ";", 2)[0])
	if !s.needsWarning(r2, other) {
		t.Fatal("a cookie for another host must not skip the warning")
	}
	// Protected tunnels never warn.
	tun.setPolicy(Policy{Mode: "password", PasswordHash: "x"})
	if s.needsWarning(browser(), tun) {
		t.Fatal("password-protected tunnels must not warn")
	}
}

func TestIdentityHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Tund-User-Email", "spoofed@evil.test")
	h.Set("x-tund-auth", "oidc")
	h.Set("X-Other", "keep")
	stripTundHeaders(h)
	if h.Get("X-Tund-User-Email") != "" || h.Get("X-Tund-Auth") != "" || h.Get("X-Other") != "keep" {
		t.Fatalf("visitor X-Tund-* headers must be stripped: %v", h)
	}
	id := &identity{Method: "oidc", Email: "a@b.com", Name: "Ada\r\nX-Evil: 1", Groups: capGroups([]string{"eng", "a,b", ""}), EmailVerified: "true", Sub: "123"}
	setIdentityHeaders(h, id)
	if h.Get("X-Tund-User-Name") != "AdaX-Evil: 1" || h.Get("X-Tund-User-Groups") != "eng,ab" || h.Get("X-Tund-User-Email") != "a@b.com" || h.Get("X-Tund-Auth") != "oidc" {
		t.Fatalf("identity headers: %v", h)
	}
	if got := identityFromHeaders(h); got == nil || got.Email != "a@b.com" || len(got.Groups) != 2 {
		t.Fatalf("round trip: %+v", got)
	}
	p := Policy{Mode: "oidc", Allow: []string{"group:eng", "boss@corp.io"}}
	if !p.Allowed("x@y.z", []string{"eng"}) || p.Allowed("x@y.z", []string{"Eng"}) || !p.Allowed("boss@corp.io", nil) || p.Allowed("", nil) {
		t.Fatal("group allow list")
	}
}

// Runs against a real database: TUND_TEST_DATABASE_URL=postgres://… go test ./internal/server -run TeamSSO
func TestTeamSSOPostgres(t *testing.T) {
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
	var uid, teamID, provID string
	if err := st.pool.QueryRow(ctx, `insert into users (email, password_hash) values ('sso-test@example.com', 'x') returning id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	defer st.pool.Exec(ctx, `delete from users where id = $1`, uid)
	if err := st.pool.QueryRow(ctx, `insert into teams (name, slug, created_by) values ('SSO', 'sso-test', $1) returning id`, uid).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	defer st.pool.Exec(ctx, `delete from teams where id = $1`, teamID)
	if err := st.pool.QueryRow(ctx, `insert into oidc_providers (user_id, team_id, name, slug, issuer, client_id)
		values ($1, $2, 'Entra', 'entra', 'https://login.example', 'cid') returning id`, uid, teamID).Scan(&provID); err != nil {
		t.Fatal(err)
	}

	sso, err := st.TeamSSO(ctx, teamID)
	if err != nil || sso == nil || sso.Required || sso.Slug != "sso-test" {
		t.Fatalf("default: %+v, %v", sso, err)
	}
	if _, err := st.pool.Exec(ctx, `update teams set auth_oidc_required = true, auth_oidc_provider_id = $2, auth_oidc_allow = '{@example.com}' where id = $1`, teamID, provID); err != nil {
		t.Fatal(err)
	}
	sso, err = st.TeamSSO(ctx, teamID)
	if err != nil || !sso.Required || sso.ProviderID != provID || len(sso.Allow) != 1 || sso.Allow[0] != "@example.com" {
		t.Fatalf("required: %+v, %v", sso, err)
	}
	// Deleting the provider leaves the requirement without one: the edge fails closed.
	st.pool.Exec(ctx, `delete from oidc_providers where id = $1`, provID)
	sso, err = st.TeamSSO(ctx, teamID)
	if err != nil || !sso.Required || sso.ProviderID != "" {
		t.Fatalf("provider deleted: %+v, %v", sso, err)
	}
	if pol, _ := effectivePolicy(&Domain{TeamID: teamID, AuthMode: "none"}, sso, nil); pol.Mode != "oidc" || pol.ProviderID != "" {
		t.Fatalf("must fail closed, got %+v", pol)
	}
	if sso, err := st.TeamSSO(ctx, "00000000-0000-0000-0000-000000000000"); err != nil || sso != nil {
		t.Fatalf("unknown team: %+v, %v", sso, err)
	}
}
