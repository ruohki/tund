package client

import "testing"

func TestParseLocalAddr(t *testing.T) {
	ok := map[string]string{
		"3000":                    "http://localhost:3000",
		" 8080 ":                  "http://localhost:8080",
		":5173":                   "http://localhost:5173",
		"127.0.0.1:9000":          "http://127.0.0.1:9000",
		"myhost:80":               "http://myhost:80",
		"[::1]:3000":              "http://[::1]:3000",
		"http://localhost:3000":   "http://localhost:3000",
		"http://localhost:3000/":  "http://localhost:3000",
		"https://localhost:8443":  "https://localhost:8443",
		"HTTPS://LocalHost":       "https://LocalHost:443",
		"http://example.internal": "http://example.internal:80",
		"https://[::1]:8443":      "https://[::1]:8443",
	}
	for in, want := range ok {
		got, err := ParseLocalAddr(in)
		if err != nil || got != want {
			t.Errorf("ParseLocalAddr(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"", "0", "70000", "localhost", "ftp://localhost:21", "tcp://x:1", "http://localhost:3000/app",
		"http://u:p@localhost:3000", "http://:3000", "localhost:http", "http://localhost:0", "http://localhost:3000?x=1"}
	for _, in := range bad {
		if got, err := ParseLocalAddr(in); err == nil {
			t.Errorf("ParseLocalAddr(%q) = %q, want error", in, got)
		}
	}
}

func TestNormalizeServer(t *testing.T) {
	ok := map[string]string{
		"dashboard.example.com":                  "https://dashboard.example.com",
		"https://Dashboard.Example.com/":         "https://dashboard.example.com",
		"http://localhost:8080":                  "http://localhost:8080",
		"wss://dashboard.example.com/_tund/ws":   "https://dashboard.example.com",
		"https://example.com/tund/":              "https://example.com/tund",
		"dashboard.example.com:8443":             "https://dashboard.example.com:8443",
		"https://dashboard.example.com/?x=1#top": "https://dashboard.example.com",
	}
	for in, want := range ok {
		got, err := NormalizeServer(in)
		if err != nil || got != want {
			t.Errorf("NormalizeServer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "ftp://x.com", "https://", "   "} {
		if got, err := NormalizeServer(in); err == nil {
			t.Errorf("NormalizeServer(%q) = %q, want error", in, got)
		}
	}
}

func TestConnectURL(t *testing.T) {
	cases := map[string]string{
		"dashboard.example.com":        "wss://dashboard.example.com/_tund/ws",
		"http://localhost:8080":        "ws://localhost:8080/_tund/ws",
		"https://example.com/tund":     "wss://example.com/tund/_tund/ws",
		"wss://x.example.com/_tund/ws": "wss://x.example.com/_tund/ws",
	}
	for in, want := range cases {
		got, err := ConnectURL(in)
		if err != nil || got != want {
			t.Errorf("ConnectURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestParseTarget(t *testing.T) {
	lt, err := parseTarget("https://localhost:8443")
	if err != nil || !lt.tls || lt.addr != "localhost:8443" || lt.serverName != "localhost" {
		t.Fatalf("parseTarget https: %+v %v", lt, err)
	}
	lt, err = parseTarget("http://[::1]:3000")
	if err != nil || lt.tls || lt.addr != "[::1]:3000" {
		t.Fatalf("parseTarget ipv6: %+v %v", lt, err)
	}
}

func TestLabels(t *testing.T) {
	if h := hostOf("https://Brave-Otter-12.tund.io"); h != "brave-otter-12.tund.io" {
		t.Fatalf("hostOf = %q", h)
	}
	if l := firstLabel("brave-otter-12.tund.io"); l != "brave-otter-12" {
		t.Fatalf("firstLabel = %q", l)
	}
}
