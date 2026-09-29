package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// fakeCallback simulates a server with loopback callback support.
type fakeCallback struct {
	mu       sync.Mutex
	noCB     bool   // an older server: ignores the callback fields
	poll     int    // status the poll answers with
	redeemed string // callback code accepted
	gotCode  map[string]any
}

func (f *fakeCallback) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_tund/device/code", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		json.NewDecoder(r.Body).Decode(&f.gotCode)
		f.mu.Unlock()
		done := `,"callback_done_url":"http://dash.example/device/done"`
		if f.noCB {
			done = ""
		}
		w.Write([]byte(`{"device_code":"dc1","user_code":"BCDF-GHJK","verification_url":"http://dash.example/device","interval":1,"expires_in":60` + done + `}`))
	})
	mux.HandleFunc("POST /_tund/device/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch code := body["callback_code"]; {
		case code == "":
			w.WriteHeader(f.poll)
			w.Write([]byte(`{"status":"approved","account":"poll@example.com"}`))
		case code == "good":
			f.redeemed = code
			w.Write([]byte(`{"status":"approved","account":"me@example.com"}`))
		case code == "gone":
			w.WriteHeader(410)
		default:
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_callback_code"}`))
		}
	})
	return mux
}

// browser follows the dashboard's redirect to the CLI, without following the
// CLI's redirect back to the dashboard.
var browser = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func visit(t *testing.T, dl *DeviceLogin, query string) *http.Response {
	t.Helper()
	resp, err := browser.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?%s", dl.cb.port(), query))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestBrowserLogin(t *testing.T) {
	old := pollUnit
	pollUnit = 20 * time.Millisecond
	defer func() { pollUnit = old }()

	f := &fakeCallback{poll: 428}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	dl, err := StartBrowserLogin(context.Background(), ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !dl.Callback() {
		t.Fatal("Callback() = false")
	}
	if port, _ := f.gotCode["callback_port"].(float64); int(port) != dl.cb.port() || f.gotCode["callback_state"] != dl.cb.state {
		t.Fatalf("code request %+v, listening on %d", f.gotCode, dl.cb.port())
	}

	type result struct {
		res *LoginResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := dl.Wait(context.Background(), nil)
		done <- result{res, err}
	}()

	state := "state=" + dl.cb.state
	for query, want := range map[string]int{
		"state=wrong&code=good": 400, // another page guessing the port
		state:                   400, // no code
		state + "&code=stale":   502, // an older tab: approve again
	} {
		if resp := visit(t, dl, query); resp.StatusCode != want {
			t.Errorf("%s: status %d, want %d", query, resp.StatusCode, want)
		}
	}
	select {
	case r := <-done:
		t.Fatalf("login ended early: %+v %v", r.res, r.err)
	case <-time.After(3 * pollUnit):
	}

	resp := visit(t, dl, state+"&code=good")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "http://dash.example/device/done" {
		t.Fatalf("callback: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	r := <-done
	if r.err != nil || r.res.Account != "me@example.com" || TokenHash(r.res.Token) != f.gotCode["token_hash"] {
		t.Fatalf("result %+v %v", r.res, r.err)
	}
	if _, err := browser.Get(fmt.Sprintf("http://127.0.0.1:%d/callback", dl.cb.port())); err == nil {
		t.Error("callback listener still open after the login")
	}
}

func TestBrowserLoginEnds(t *testing.T) {
	old := pollUnit
	pollUnit = 20 * time.Millisecond
	defer func() { pollUnit = old }()

	// Denied in the browser: the poll reports it.
	f := &fakeCallback{poll: 403}
	ts := httptest.NewServer(f.handler(t))
	dl, err := StartBrowserLogin(context.Background(), ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dl.Wait(context.Background(), nil); !errors.Is(err, ErrLoginDenied) {
		t.Errorf("denied: err = %v", err)
	}
	ts.Close()

	// Expired by the time the browser arrives.
	f = &fakeCallback{poll: 428}
	ts = httptest.NewServer(f.handler(t))
	defer ts.Close()
	dl, err = StartBrowserLogin(context.Background(), ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	go browser.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?state=%s&code=gone", dl.cb.port(), dl.cb.state))
	if _, err := dl.Wait(context.Background(), nil); !errors.Is(err, ErrLoginExpired) {
		t.Errorf("expired: err = %v", err)
	}

	// Approved through a dashboard without callback support: the poll wins.
	f = &fakeCallback{poll: 200}
	ts2 := httptest.NewServer(f.handler(t))
	defer ts2.Close()
	dl, err = StartBrowserLogin(context.Background(), ts2.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := dl.Wait(context.Background(), nil); err != nil || res.Account != "poll@example.com" {
		t.Errorf("poll: %+v %v", res, err)
	}
}

func TestBrowserLoginOldServer(t *testing.T) {
	f := &fakeCallback{noCB: true, poll: 200}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()
	dl, err := StartBrowserLogin(context.Background(), ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dl.Callback() {
		t.Fatal("Callback() = true without callback_done_url")
	}
	port := int(f.gotCode["callback_port"].(float64))
	if _, err := browser.Get(fmt.Sprintf("http://127.0.0.1:%d/callback", port)); err == nil {
		t.Error("unused callback listener still open")
	}
}
