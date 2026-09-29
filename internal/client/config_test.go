package client

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"tund/internal/protocol"
)

func TestConfigRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "tund.yml")
	c, err := LoadConfig(p)
	if err != nil || c.Server != "" || len(c.Tunnels) != 0 {
		t.Fatalf("missing file should give empty config: %+v %v", c, err)
	}
	c.Server = "https://dashboard.example.com"
	c.Authtoken = "tund_abc"
	c.Tunnels = map[string]*TunnelConfig{
		"web": {Addr: "3000", Subdomain: "myapp"},
		"api": {Addr: "https://localhost:8443", Domain: "api.example.com", HostHeader: "rewrite",
			Auth: &AuthConfig{OIDC: "google", Allow: []string{"@example.com"}}},
	}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(p)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("config perms = %v, %v", st.Mode().Perm(), err)
		}
	}
	c2, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Server != c.Server || c2.Authtoken != c.Authtoken || len(c2.Tunnels) != 2 ||
		c2.Tunnels["api"].Auth.OIDC != "google" || c2.Tunnels["web"].Subdomain != "myapp" {
		t.Fatalf("round trip mismatch: %+v", c2)
	}
}

func TestConfigNumericAddr(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tund.yml")
	yml := "server: dashboard.example.com\ntunnels:\n  web:\n    addr: 3000\n    auth:\n      password: pw\n"
	if err := os.WriteFile(p, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := c.Tunnels["web"].Spec("web")
	if err != nil {
		t.Fatal(err)
	}
	if spec.LocalAddr != "http://localhost:3000" || spec.Auth == nil || spec.Auth.Mode != protocol.AuthPassword {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestConfigInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tund.yml")
	os.WriteFile(p, []byte("tunnels: [oops"), 0o600)
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := (&TunnelConfig{}).Spec("x"); err == nil {
		t.Fatal("expected missing addr error")
	}
	if _, err := (&TunnelConfig{Addr: "1", Subdomain: "a", Domain: "b.com"}).Spec("x"); err == nil {
		t.Fatal("expected subdomain/domain conflict")
	}
}

func TestBuildAuth(t *testing.T) {
	if a, err := BuildAuth("", "", nil); a != nil || err != nil {
		t.Fatalf("no auth: %+v %v", a, err)
	}
	a, err := BuildAuth("", "google", []string{"A@B.com, @corp.com", " "})
	if err != nil || a.Mode != protocol.AuthOIDC || len(a.Allow) != 2 || a.Allow[0] != "a@b.com" {
		t.Fatalf("oidc: %+v %v", a, err)
	}
	for _, bad := range [][3]any{{"pw", "google", nil}, {"pw", "", []string{"@x.com"}}, {"", "", []string{"@x.com"}}} {
		allow, _ := bad[2].([]string)
		if _, err := BuildAuth(bad[0].(string), bad[1].(string), allow); err == nil {
			t.Fatalf("expected error for %v", bad)
		}
	}
}

func TestState(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s := LoadState(p)
	if err := s.Set("k", "brave-otter-1"); err != nil {
		t.Fatal(err)
	}
	if got := LoadState(p).Get("k"); got != "brave-otter-1" {
		t.Fatalf("state = %q", got)
	}
	s.Set("k", "")
	if got := LoadState(p).Get("k"); got != "" {
		t.Fatalf("state after delete = %q", got)
	}
	os.WriteFile(p, []byte("garbage"), 0o600)
	if got := LoadState(p).Get("k"); got != "" {
		t.Fatalf("corrupt state = %q", got)
	}
	var nilState *State
	if nilState.Get("x") != "" || nilState.Set("x", "y") != nil {
		t.Fatal("nil state must be a no-op")
	}
}

func TestDefaultConfigPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	if p, _ := DefaultConfigPath(); p != "/tmp/xdg/tund/tund.yml" {
		t.Fatalf("xdg path = %q", p)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	home, _ := os.UserHomeDir()
	if p, _ := DefaultConfigPath(); p != filepath.Join(home, ".config", "tund", "tund.yml") {
		t.Fatalf("default path = %q", p)
	}
}

func TestBackoff(t *testing.T) {
	for i := 0; i < 20; i++ {
		d := backoff(i)
		if d <= 0 || d > 33e9 {
			t.Fatalf("backoff(%d) = %v", i, d)
		}
	}
}

func TestNormalizeAllow(t *testing.T) {
	cases := map[string]string{
		" Bob@Example.COM ":    "bob@example.com",
		"@Corp.com":            "@corp.com",
		"group:Engineering":    "group:Engineering",
		"group: Platform Team": "group:Platform Team",
		"group:":               "",
		"  ":                   "",
	}
	for in, want := range cases {
		if got := NormalizeAllow(in); got != want {
			t.Errorf("NormalizeAllow(%q) = %q, want %q", in, got, want)
		}
	}
	a, err := BuildAuth("", "acme/okta", []string{"group:Admins,@X.com"})
	if err != nil || a.Provider != "acme/okta" || len(a.Allow) != 2 || a.Allow[0] != "group:Admins" || a.Allow[1] != "@x.com" {
		t.Fatalf("BuildAuth = %+v, %v", a, err)
	}
}
