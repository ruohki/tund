package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"tund/internal/protocol"
)

func TestCompileRulesErrors(t *testing.T) {
	bad := []protocol.Rules{
		{RequestHeaders: &protocol.HeaderRules{Set: map[string]string{"Host": "x"}}},
		{RequestHeaders: &protocol.HeaderRules{Remove: []string{"X-Tund-User-Email"}}},
		{ResponseHeaders: &protocol.HeaderRules{Set: map[string]string{"Bad Name": "x"}}},
		{ResponseHeaders: &protocol.HeaderRules{Set: map[string]string{"X-A": "line\r\nbreak"}}},
		{CORS: &protocol.CORS{}},
		{CORS: &protocol.CORS{Origins: []string{"*"}, Credentials: true}},
		{CORS: &protocol.CORS{Origins: []string{"https://a.example.com/path"}}},
		{RateLimit: "100"},
		{RateLimit: "0/m"},
		{RateLimit: "10/d"},
		{Routes: []protocol.Route{{Path: "/", LocalAddr: "http://localhost:1"}}},
		{Routes: []protocol.Route{{Path: "api", LocalAddr: "http://localhost:1"}}},
		{Routes: []protocol.Route{{Path: "/api", LocalAddr: "localhost:1"}}},
		{Routes: []protocol.Route{{Path: "/api", LocalAddr: "http://localhost:1"}, {Path: "/api/", LocalAddr: "http://localhost:2"}}},
	}
	for i, r := range bad {
		if _, err := compileRules(&r, ""); err == nil {
			t.Errorf("case %d: %+v compiled", i, r)
		} else if _, ok := err.(bindError); !ok {
			t.Errorf("case %d: not a bindError: %v", i, err)
		}
	}
	if r, err := compileRules(&protocol.Rules{}, ""); r != nil || err != nil {
		t.Errorf("empty rules = %v, %v", r, err)
	}
}

func TestRoutes(t *testing.T) {
	r, err := compileRules(&protocol.Rules{Routes: []protocol.Route{
		{Path: "/api", LocalAddr: "http://localhost:8080"},
		{Path: "/api/v2/", LocalAddr: "https://127.0.0.1:9443", StripPrefix: true},
	}}, "rewrite")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{"/": 0, "/apix": 0, "/api": 1, "/api/users": 1, "/api/v2": 2, "/api/v2/x": 2} {
		got := 0
		if rt := r.route(path); rt != nil {
			got = rt.index
		}
		if got != want {
			t.Errorf("route(%q) = %d, want %d", path, got, want)
		}
	}
	rt := r.route("/api/v2/items")
	if rt.hostHeader != "127.0.0.1:9443" {
		t.Errorf("host header for rewrite = %q", rt.hostHeader)
	}
	u, _ := url.Parse("/api/v2/items%2Fx?q=1")
	rt.stripPath(u)
	if u.Path != "/items/x" || u.RawQuery != "q=1" {
		t.Errorf("stripped = %q %q (raw %q)", u.Path, u.RawQuery, u.RawPath)
	}
	u, _ = url.Parse("/api/v2")
	rt.stripPath(u)
	if u.Path != "/" {
		t.Errorf("stripped exact prefix = %q", u.Path)
	}
	if r.route("/api/x").strip {
		t.Error("route 1 must not strip")
	}
	for i := 0; i <= 3; i++ {
		if got := routeOfAddr(routeHost(i) + ":80"); i > 0 && got != i {
			t.Errorf("routeOfAddr(routeHost(%d)) = %d", i, got)
		}
	}
	if routeOfAddr("myapp.tund.io:80") != 0 {
		t.Error("tunnel host must map to route 0")
	}
}

func TestHeaderAndCORSRules(t *testing.T) {
	r, err := compileRules(&protocol.Rules{
		RequestHeaders:  &protocol.HeaderRules{Set: map[string]string{"x-env": "preview"}, Remove: []string{"cookie"}},
		ResponseHeaders: &protocol.HeaderRules{Set: map[string]string{"X-Frame-Options": "DENY"}, Remove: []string{"Server"}},
		CORS:            &protocol.CORS{Origins: []string{"https://App.example.com/"}, Credentials: true, Expose: []string{"X-Total"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	req := http.Header{"Cookie": {"a=1"}, "X-Env": {"prod"}}
	r.applyRequest(req)
	if req.Get("Cookie") != "" || req.Get("X-Env") != "preview" {
		t.Errorf("request headers = %v", req)
	}
	resp := http.Header{"Server": {"nginx"}, "Access-Control-Allow-Origin": {"*"}}
	r.applyResponse(resp, "https://app.example.com")
	if resp.Get("Server") != "" || resp.Get("X-Frame-Options") != "DENY" || resp.Get("Access-Control-Allow-Origin") != "https://app.example.com" ||
		resp.Get("Access-Control-Allow-Credentials") != "true" || resp.Get("Access-Control-Expose-Headers") != "X-Total" || resp.Get("Vary") != "Origin" {
		t.Errorf("response headers = %v", resp)
	}
	resp = http.Header{"Access-Control-Allow-Origin": {"*"}}
	r.applyResponse(resp, "https://evil.example.com")
	if resp.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("foreign origin got %v", resp)
	}

	pre := httptest.NewRequest(http.MethodOptions, "/x", nil)
	pre.Header.Set("Origin", "https://app.example.com")
	pre.Header.Set("Access-Control-Request-Method", "PUT")
	pre.Header.Set("Access-Control-Request-Headers", "content-type, x-token")
	if !isPreflight(pre) {
		t.Fatal("not detected as preflight")
	}
	w := httptest.NewRecorder()
	r.cors.preflight(w, pre)
	h := w.Result().Header
	if w.Code != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != "https://app.example.com" ||
		h.Get("Access-Control-Allow-Headers") != "content-type, x-token" || !strings.Contains(h.Get("Access-Control-Allow-Methods"), "PUT") ||
		h.Get("Access-Control-Max-Age") != "600" {
		t.Errorf("preflight = %d %v", w.Code, h)
	}

	star, _ := compileRules(&protocol.Rules{CORS: &protocol.CORS{Origins: []string{"*"}}}, "")
	if o, ok := star.cors.allow("https://anything.example"); !ok || o != "*" {
		t.Errorf("* allows %q %v", o, ok)
	}
}

func TestVisitorLimiter(t *testing.T) {
	l := newVisitorLimiter(3, time.Minute)
	if l.label != "3 requests per minute" {
		t.Errorf("label = %q", l.label)
	}
	now := time.Now()
	for i := range 3 {
		if !l.allow("1.2.3.4", now) {
			t.Fatalf("request %d rejected", i)
		}
	}
	if l.allow("1.2.3.4", now) {
		t.Fatal("4th request allowed")
	}
	if !l.allow("5.6.7.8", now) {
		t.Fatal("other visitor rejected")
	}
	if !l.allow("1.2.3.4", now.Add(20*time.Second)) {
		t.Fatal("token not refilled after 20s")
	}
	if got := l.retryAfter(); got != "20" {
		t.Errorf("retryAfter = %s", got)
	}
	if n := l.noteRejected(now); n != 1 {
		t.Errorf("first notice reports %d", n)
	}
	l.noteRejected(now.Add(time.Second))
	l.noteRejected(now.Add(2 * time.Second))
	if n := l.noteRejected(now.Add(61 * time.Second)); n != 3 {
		t.Errorf("second notice reports %d", n)
	}
	// Idle visitors are forgotten.
	l.allow("9.9.9.9", now.Add(5*time.Minute))
	if _, ok := l.byIP["5.6.7.8"]; ok {
		t.Error("idle visitor kept")
	}
}

func TestParseRateLimit(t *testing.T) {
	for in, want := range map[string]time.Duration{"10/s": time.Second, "100 / m": time.Minute, "5000/hour": time.Hour} {
		if _, per, err := parseRateLimit(in); err != nil || per != want {
			t.Errorf("parseRateLimit(%q) = %v, %v", in, per, err)
		}
	}
}
