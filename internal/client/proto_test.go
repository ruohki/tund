package client

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tund/internal/protocol"
)

func TestParseTCPAddr(t *testing.T) {
	ok := map[string]string{
		"5432": "localhost:5432", ":22": "localhost:22", "db:5432": "db:5432",
		"tcp://10.0.0.5:3389": "10.0.0.5:3389", "TCP://db:1": "db:1", "[::1]:8443": "[::1]:8443",
	}
	for in, want := range ok {
		if got, err := ParseTCPAddr(in); err != nil || got != want {
			t.Errorf("ParseTCPAddr(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "70000", "http://localhost:3000", "https://x:1", "db", "db:x"} {
		if got, err := ParseTCPAddr(in); err == nil {
			t.Errorf("ParseTCPAddr(%q) = %q, want error", in, got)
		}
	}
	if got, _ := ParseTarget("tls", "8443"); got != "localhost:8443" {
		t.Errorf("ParseTarget tls = %q", got)
	}
	if got, _ := ParseTarget("", "3000"); got != "http://localhost:3000" {
		t.Errorf("ParseTarget http = %q", got)
	}
	if _, err := ParseTarget("udp", "53"); err == nil {
		t.Error("ParseTarget udp must fail")
	}
}

func TestNormalizeAllowIPs(t *testing.T) {
	got, err := NormalizeAllowIPs([]string{" 203.0.113.7 ,10.1.2.3/8", "2001:DB8::1", "2001:db8::/32"})
	want := []string{"203.0.113.7", "10.0.0.0/8", "2001:db8::1", "2001:db8::/32"}
	if err != nil || strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("NormalizeAllowIPs = %v, %v", got, err)
	}
	for _, bad := range []string{"example.com", "10.0.0.0/33", "300.1.1.1", "1.2.3.4/"} {
		if _, err := NormalizeAllowIPs([]string{bad}); err == nil {
			t.Errorf("NormalizeAllowIPs(%q) should fail", bad)
		}
	}
	if got, err := NormalizeAllowIPs(nil); err != nil || got != nil {
		t.Errorf("empty = %v, %v", got, err)
	}
}

func TestValidate(t *testing.T) {
	good := []TunnelSpec{
		{Proto: "", Subdomain: "x"},
		{Proto: "tcp", RemotePort: 20000, Pin: true},
		{Proto: "tls", Subdomain: "x", Pin: true},
		{Proto: "tls", Random: true, TerminateCert: "c", TerminateKey: "k"},
		{Proto: "http", Auth: &protocol.Auth{Mode: "password", Password: "p"}},
	}
	for _, s := range good {
		if err := s.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", s, err)
		}
	}
	bad := []TunnelSpec{
		{Proto: "tcp", Subdomain: "x"},
		{Proto: "tcp", Random: true},
		{Proto: "tcp", Auth: &protocol.Auth{Mode: "password"}},
		{Proto: "tcp", HostHeader: "rewrite"},
		{Proto: "tcp", TerminateCert: "c", TerminateKey: "k"},
		{Proto: "tls", RemotePort: 1},
		{Proto: "tls", Auth: &protocol.Auth{Mode: "oidc"}},
		{Proto: "tls", TerminateCert: "c"},
		{Proto: "http", RemotePort: 1},
		{Proto: "http", TerminateCert: "c", TerminateKey: "k"},
		{Proto: "http", Random: true, Subdomain: "x"},
		{Proto: "udp"},
	}
	for _, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v) should fail", s)
		}
	}
}

func TestValidateHandler(t *testing.T) {
	base := handlerSpec(http.NotFoundHandler())
	with := func(change func(*TunnelSpec)) TunnelSpec {
		s := base
		change(&s)
		return s
	}
	good := []TunnelSpec{
		base,
		with(func(s *TunnelSpec) {
			s.Auth = &protocol.Auth{Mode: protocol.AuthOIDC, Provider: "google", Allow: []string{"@example.com"}}
		}),
		with(func(s *TunnelSpec) { s.Proto, s.Random, s.Pin = "http", true, true }),
		with(func(s *TunnelSpec) {
			s.Subdomain, s.AllowIPs, s.RestartOnExpiry = "files", []string{"203.0.113.7"}, true
		}),
		with(func(s *TunnelSpec) { s.LocalAddr = "https://file-share:443" }),
	}
	for _, s := range good {
		if err := s.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", s, err)
		}
	}
	bad := map[string]TunnelSpec{
		"no auth":         with(func(s *TunnelSpec) { s.Auth = nil }),
		"auth none":       with(func(s *TunnelSpec) { s.Auth = &protocol.Auth{Mode: protocol.AuthNone} }),
		"empty auth mode": with(func(s *TunnelSpec) { s.Auth = &protocol.Auth{Password: "correct-horse"} }),
		"tcp":             with(func(s *TunnelSpec) { s.Proto = "tcp" }),
		"tls":             with(func(s *TunnelSpec) { s.Proto = "tls" }),
		"rules":           with(func(s *TunnelSpec) { s.Rules = &protocol.Rules{RateLimit: "10/s"} }),
		"pool":            with(func(s *TunnelSpec) { s.Pool, s.Subdomain = true, "files" }),
		"host header":     with(func(s *TunnelSpec) { s.HostHeader = "rewrite" }),
		"tls termination": with(func(s *TunnelSpec) { s.TerminateCert, s.TerminateKey = "c.pem", "k.pem" }),
		"no label":        with(func(s *TunnelSpec) { s.LocalAddr = "" }),
		"path as label":   with(func(s *TunnelSpec) { s.LocalAddr = "/home/alice/share" }),
		"file URL":        with(func(s *TunnelSpec) { s.LocalAddr = "file:///home/alice/share" }),
		"label sans host": with(func(s *TunnelSpec) { s.LocalAddr = "http://" }),
	}
	for name, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: Validate should fail", name)
		}
		if _, err := New(Options{Server: "https://tund.example.com", Authtoken: "tund_x", Tunnels: []TunnelSpec{s}, Events: func(Event) {}}); err == nil {
			t.Errorf("%s: New should fail", name)
		}
	}
}

func TestTCPBindMessage(t *testing.T) {
	// Remembered port is a preference (auto).
	tn := &tunnel{spec: TunnelSpec{Name: "db", Proto: "tcp", LocalAddr: "localhost:5432", AllowIPs: []string{"10.0.0.0/8"}}, port: 20417}
	b := bindFor(t, tn)
	if b.Proto != "tcp" || b.RemotePort != 20417 || !b.Auto || b.Subdomain != "" || len(b.AllowIPs) != 1 {
		t.Errorf("remembered tcp: %+v", b)
	}
	// Explicit port is never auto.
	b = bindFor(t, &tunnel{spec: TunnelSpec{Name: "db", Proto: "tcp", LocalAddr: "localhost:5432", RemotePort: 20000, Pin: true}, port: 20417})
	if b.RemotePort != 20000 || b.Auto || !b.Pin {
		t.Errorf("explicit tcp: %+v", b)
	}
	// tls uses labels like http; http leaves proto empty for older servers.
	b = bindFor(t, &tunnel{spec: TunnelSpec{Name: "s", Proto: "tls", LocalAddr: "localhost:8443"}, label: "calm-owl-1"})
	if b.Proto != "tls" || b.Subdomain != "calm-owl-1" || !b.Auto || b.RemotePort != 0 {
		t.Errorf("tls: %+v", b)
	}
	b = bindFor(t, &tunnel{spec: TunnelSpec{Name: "w", LocalAddr: "http://localhost:3000"}})
	if b.Proto != "" {
		t.Errorf("http proto = %q", b.Proto)
	}
}

func TestStatePorts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s := LoadState(p)
	if err := s.SetPort("k", 20417); err != nil {
		t.Fatal(err)
	}
	s.Set("l", "brave-owl-1")
	s2 := LoadState(p)
	if s2.GetPort("k") != 20417 || s2.Get("l") != "brave-owl-1" {
		t.Fatalf("state = %+v", s2)
	}
	s2.SetPort("k", 0)
	if LoadState(p).GetPort("k") != 0 {
		t.Fatal("port not forgotten")
	}
	os.WriteFile(p, []byte(`{"subdomains":{"a":"b"}}`), 0o600) // old format
	if s3 := LoadState(p); s3.Get("a") != "b" || s3.SetPort("x", 1) != nil {
		t.Fatal("old state format")
	}
}

func TestConfigRawTunnels(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tund.yml")
	yml := `tunnels:
  db:
    proto: tcp
    addr: 5432
    remote_port: 20432
    allow_ips: ["203.0.113.0/24", "10.0.0.1"]
  mqtt:
    proto: tls
    addr: 8883
    terminate_cert: certs/c.pem
    terminate_key: /abs/k.pem
  bad:
    proto: tcp
    addr: 22
    subdomain: nope
  badip:
    addr: 3000
    allow_ips: [nope]
`
	os.WriteFile(p, []byte(yml), 0o600)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	db, err := cfg.TunnelSpec("db", p)
	if err != nil || db.Proto != "tcp" || db.LocalAddr != "localhost:5432" || db.RemotePort != 20432 || len(db.AllowIPs) != 2 {
		t.Fatalf("db = %+v, %v", db, err)
	}
	mq, err := cfg.TunnelSpec("mqtt", p)
	if err != nil || mq.TerminateCert != filepath.Join(dir, "certs", "c.pem") || mq.TerminateKey != "/abs/k.pem" {
		t.Fatalf("mqtt = %+v, %v", mq, err)
	}
	for _, n := range []string{"bad", "badip", "missing"} {
		if _, err := cfg.TunnelSpec(n, p); err == nil {
			t.Errorf("%s should fail", n)
		}
	}
}

func TestTerminationConfigErrors(t *testing.T) {
	if cfg, err := terminationConfig("", ""); cfg != nil || err != nil {
		t.Fatal("no termination expected")
	}
	if _, err := terminationConfig("/nonexistent.pem", "/nonexistent-key.pem"); err == nil {
		t.Fatal("missing files must fail")
	}
	if humanBytes(10) != "10 B" || humanBytes(2048) != "2.0 KB" || humanBytes(5<<20) != "5.0 MB" {
		t.Fatal(humanBytes(2048))
	}
}
