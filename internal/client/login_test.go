package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGenerateToken(t *testing.T) {
	re := regexp.MustCompile(`^tund_[0-9a-f]{40}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		tok, err := GenerateToken()
		if err != nil || !re.MatchString(tok) {
			t.Fatalf("GenerateToken() = %q, %v", tok, err)
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
	}
	tok := "tund_0123456789abcdef0123456789abcdef01234567"
	if p := TokenPrefix(tok); p != "tund_01234567" {
		t.Fatalf("TokenPrefix = %q", p)
	}
	// sha256("tund_0123456789abcdef0123456789abcdef01234567")
	if h := TokenHash(tok); len(h) != 64 || strings.ToLower(h) != h {
		t.Fatalf("TokenHash = %q", h)
	}
	if TokenHash("abc") != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("TokenHash is not sha256 hex")
	}
}

func TestEndpointURL(t *testing.T) {
	cases := map[string]string{
		"tund.io":                      "https://tund.io/_tund/device/code",
		"https://tund.example.com/":    "https://tund.example.com/_tund/device/code",
		"http://localhost:8080":        "http://localhost:8080/_tund/device/code",
		"https://example.com/sub/":     "https://example.com/sub/_tund/device/code",
		"wss://x.example.com/_tund/ws": "https://x.example.com/_tund/device/code",
	}
	for in, want := range cases {
		if got, err := EndpointURL(in, deviceCodePath); err != nil || got != want {
			t.Errorf("EndpointURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestPrecedence(t *testing.T) {
	old := DefaultServer
	defer func() { DefaultServer = old }()
	DefaultServer = "https://cloud.example"

	check := func(flag, env, cfg, want, wantSrc string) {
		t.Helper()
		got, src := ResolveServer(flag, env, cfg)
		if got != want || src != wantSrc {
			t.Errorf("ResolveServer(%q,%q,%q) = %q,%q; want %q,%q", flag, env, cfg, got, src, want, wantSrc)
		}
	}
	check("f", "e", "c", "f", SourceFlag)
	check("", "e", "c", "e", SourceEnv)
	check(" ", "", "c", "c", SourceConfig)
	check("", "", "", "https://cloud.example", SourceDefault)
	DefaultServer = ""
	check("", "", "", "", SourceNone)
	if v, src := Pick("", "", "", ""); v != "" || src != SourceNone {
		t.Errorf("Pick empty = %q %q", v, src)
	}
	if DefaultServer = old; old != "https://tund.io" {
		t.Errorf("source default DefaultServer = %q", old)
	}
}

// fakeDevice simulates /_tund/device/{code,token}.
type fakeDevice struct {
	mu        sync.Mutex
	outcome   string // approved | denied | expired
	pending   int    // polls answered with 428 before the outcome
	slowDowns int    // polls answered with 429 first
	polls     []time.Time
	gotCode   map[string]string
}

func (f *fakeDevice) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_tund/device/code", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		json.Unmarshal(b, &f.gotCode)
		f.mu.Unlock()
		w.Write([]byte(`{"device_code":"dc1","user_code":"BCDF-GHJK","verification_url":"http://x/device","verification_url_complete":"http://x/device?code=BCDF-GHJK","interval":1,"expires_in":60}`))
	})
	mux.HandleFunc("POST /_tund/device/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["device_code"] != "dc1" {
			t.Errorf("device_code = %q", body["device_code"])
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.polls = append(f.polls, time.Now())
		switch {
		case f.slowDowns > 0:
			f.slowDowns--
			w.WriteHeader(429)
			w.Write([]byte(`{"error":"slow_down"}`))
		case f.pending > 0:
			f.pending--
			w.WriteHeader(428)
			w.Write([]byte(`{"error":"authorization_pending"}`))
		case f.outcome == "approved":
			w.Write([]byte(`{"status":"approved","account":"me@example.com"}`))
		case f.outcome == "denied":
			w.WriteHeader(403)
			w.Write([]byte(`{"error":"access_denied"}`))
		default:
			w.WriteHeader(410)
			w.Write([]byte(`{"error":"expired_token"}`))
		}
	})
	return mux
}

func TestLoginFlow(t *testing.T) {
	old := pollUnit
	pollUnit = 20 * time.Millisecond
	defer func() { pollUnit = old }()
	quiet := &Display{out: io.Discard, said: map[string]bool{}}

	f := &fakeDevice{outcome: "approved", pending: 2, slowDowns: 1}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	res, err := Login(context.Background(), LoginOptions{Server: ts.URL, Display: quiet, NoBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Account != "me@example.com" || TokenHash(res.Token) != f.gotCode["token_hash"] || TokenPrefix(res.Token) != f.gotCode["token_prefix"] {
		t.Fatalf("result %+v, code request %+v", res, f.gotCode)
	}
	if f.gotCode["client_os"] == "" {
		t.Error("client_os missing")
	}
	if len(f.polls) != 4 {
		t.Fatalf("polls = %d, want 4", len(f.polls))
	}
	// After the 429 the interval grows from 1 to 6 units.
	if gap := f.polls[1].Sub(f.polls[0]); gap < 5*pollUnit {
		t.Errorf("no slow-down after 429: gap %v", gap)
	}

	for outcome, want := range map[string]error{"denied": ErrLoginDenied, "expired": ErrLoginExpired} {
		f := &fakeDevice{outcome: outcome, pending: 1}
		ts := httptest.NewServer(f.handler(t))
		_, err := Login(context.Background(), LoginOptions{Server: ts.URL, Display: quiet, NoBrowser: true})
		ts.Close()
		if !errors.Is(err, want) {
			t.Errorf("%s: err = %v", outcome, err)
		}
	}

	// Cancellation.
	f = &fakeDevice{outcome: "approved", pending: 1000}
	ts2 := httptest.NewServer(f.handler(t))
	defer ts2.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Login(ctx, LoginOptions{Server: ts2.URL, Display: quiet, NoBrowser: true}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("cancel: err = %v", err)
	}

	// A server without the device endpoints.
	ts3 := httptest.NewServer(http.NotFoundHandler())
	defer ts3.Close()
	if _, err := Login(context.Background(), LoginOptions{Server: ts3.URL, Display: quiet, NoBrowser: true}); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Errorf("404: err = %v", err)
	}
}
