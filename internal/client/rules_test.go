package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildRules(t *testing.T) {
	r, err := BuildRules(RuleOptions{
		RequestSet:     []string{"X-Env: preview, staging"},
		ResponseRemove: []string{"Server,X-Powered-By"},
		CORSOrigins:    []string{"https://a.example.com"},
		RateLimit:      " 100/m ",
		Routes:         []string{"/api=8080", "/ws=https://localhost:9443"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.RequestHeaders.Set["X-Env"] != "preview, staging" || len(r.ResponseHeaders.Remove) != 2 || r.ResponseHeaders.Set != nil ||
		r.CORS.Origins[0] != "https://a.example.com" || r.RateLimit != "100/m" ||
		r.Routes[0].LocalAddr != "http://localhost:8080" || r.Routes[1].LocalAddr != "https://localhost:9443" {
		t.Fatalf("rules = %+v", r)
	}
	if got := rulesSummary(r); got != "3 header rules · CORS https://a.example.com · rate limit 100/m per visitor" {
		t.Errorf("summary = %q", got)
	}
	if r, err := BuildRules(RuleOptions{}); r != nil || err != nil {
		t.Errorf("no options = %+v, %v", r, err)
	}
	for _, o := range []RuleOptions{
		{RequestSet: []string{"no colon"}},
		{Routes: []string{"/api"}},
		{Routes: []string{"/=3000"}},
		{Routes: []string{"api=3000"}},
		{Routes: []string{"/api=ftp://x"}},
		{CORS: &CORSConfig{}},
	} {
		if _, err := BuildRules(o); err == nil {
			t.Errorf("%+v: no error", o)
		}
	}
}

func TestConfigRules(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tund.yml")
	yml := `tunnels:
  app:
    addr: 3000
    routes:
      - path: /api
        addr: 8080
        strip_prefix: true
    cors:
      origins: ["https://app.example.com"]
      credentials: true
      max_age: 60
    rate_limit: 10/s
    request_headers:
      set: {X-Env: preview}
    response_headers:
      remove: [Server]
  db:
    proto: tcp
    addr: 5432
    rate_limit: 1/s
`
	if err := os.WriteFile(p, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := c.TunnelSpec("app", p)
	if err != nil {
		t.Fatal(err)
	}
	r := spec.Rules
	if r == nil || len(r.Routes) != 1 || !r.Routes[0].StripPrefix || r.Routes[0].LocalAddr != "http://localhost:8080" ||
		!r.CORS.Credentials || r.CORS.MaxAge != 60 || r.RateLimit != "10/s" ||
		r.RequestHeaders.Set["X-Env"] != "preview" || r.ResponseHeaders.Remove[0] != "Server" {
		t.Fatalf("rules = %+v", r)
	}
	if _, err := c.TunnelSpec("db", p); err == nil {
		t.Error("rules on a tcp tunnel must fail")
	}
}
