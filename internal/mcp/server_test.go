package tundmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"tund/internal/client"
)

// fakeAPI implements the parts of the public API and device login the tools use.
type fakeAPI struct {
	mu         sync.Mutex
	token      string // accepted bearer token; "" = accept the device-login token
	approved   string // token hash approved by the device flow
	codeHash   string
	pending    int
	replayMiss int // GET /requests/r2 answers 404 this many times
	lastQuery  string
	lastReplay map[string]any
	stoppedIDs []string
}

func (f *fakeAPI) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			f.mu.Lock()
			ok := tok != "" && (tok == f.token || (f.approved != "" && client.TokenHash(tok) == f.approved))
			f.mu.Unlock()
			if !ok {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":"invalid authtoken"}`))
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /_tund/api/v1/me", authed(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"account":{"id":"u1","email":"dev@example.com","name":"Dev","is_admin":false},"server":{"base_domain":"tund.test","dashboard_url":"https://tund.test","version":"9.9"},"limits":{"tunnels":5,"pinned":2,"domains":0},"static_hostnames":[{"hostname":"quiet-fox.tund.test","url":"https://quiet-fox.tund.test","default":true}],"teams":[{"slug":"acme","name":"Acme","role":"admin"}]}`))
	}))
	mux.HandleFunc("GET /_tund/api/v1/tunnels", authed(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tunnels":[{"id":"t-9","name":"http-8080","hostname":"other.tund.test","url":"https://other.tund.test","local_addr":"http://localhost:8080","auth_mode":"none","static":false,"started_at":"2026-09-29T10:00:00Z","client":{"hostname":"laptop","os":"linux/amd64","version":"1.0"}}]}`))
	}))
	mux.HandleFunc("POST /_tund/api/v1/tunnels/{id}/stop", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.stoppedIDs = append(f.stoppedIDs, r.PathValue("id"))
		f.mu.Unlock()
		w.Write([]byte(`{}`))
	}))
	mux.HandleFunc("GET /_tund/api/v1/requests", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.lastQuery = r.URL.RawQuery
		f.mu.Unlock()
		w.Write([]byte(`{"requests":[
			{"id":"r3","hostname":"other.tund.test","method":"GET","path":"/missing","status":404,"duration_ms":3,"started_at":"2026-09-29T10:00:03Z"},
			{"id":"r2","hostname":"other.tund.test","method":"GET","path":"/forbidden","status":403,"duration_ms":2,"started_at":"2026-09-29T10:00:02Z"}
		],"next_before":"2026-09-29T10:00:02Z"}`))
	}))
	mux.HandleFunc("GET /_tund/api/v1/requests/{id}", authed(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		f.mu.Lock()
		miss := id == "r2" && f.replayMiss > 0
		if miss {
			f.replayMiss--
		}
		f.mu.Unlock()
		if id == "r4" {
			w.Write([]byte(`{"id":"r4","hostname":"other.tund.test","method":"GET","path":"/me","status":200,"proto":"HTTP/1.1","started_at":"2026-09-29T10:00:01Z",
				"request":{"headers":{"X-Tund-Auth":["oidc"],"X-Tund-User-Email":["alice@example.com"],"X-Tund-User-Name":["Alice"],"X-Tund-User-Groups":["eng,admins"],"X-Tund-Idp":["https://idp.example.com"]},"body":{"size":0}},
				"response":{"headers":{},"body":{"size":0}}}`))
			return
		}
		if miss || (id != "r1" && id != "r2") {
			w.WriteHeader(404)
			w.Write([]byte(`{"error":"request not found"}`))
			return
		}
		body := strings.Repeat("x", 50)
		replayOf := ""
		if id == "r2" {
			replayOf = "r1"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id": id, "hostname": "other.tund.test", "method": "POST", "path": "/api/items?x=1", "status": 201, "duration_ms": 12.5,
			"replay_of": replayOf, "proto": "HTTP/1.1", "started_at": "2026-09-29T10:00:01Z",
			"request": map[string]any{"headers": map[string][]string{"Content-Type": {"application/json"}},
				"body": map[string]any{"size": 50, "content_type": "application/json", "text": body}},
			"response": map[string]any{"headers": map[string][]string{"Content-Type": {"image/png"}},
				"body": map[string]any{"size": 4, "content_type": "image/png", "base64": "iVBORw=="}},
		})
	}))
	mux.HandleFunc("POST /_tund/api/v1/requests/{id}/replay", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.lastReplay = nil
		json.NewDecoder(r.Body).Decode(&f.lastReplay)
		f.mu.Unlock()
		w.Write([]byte(`{"request_id":"r2","status":201}`))
	}))
	mux.HandleFunc("GET /_tund/api/v1/connections", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.lastQuery = r.URL.RawQuery
		f.mu.Unlock()
		w.Write([]byte(`{"connections":[{"id":"c1","tunnel_id":"t2","proto":"tcp","address":"tund.test:20417","remote_addr":"203.0.113.5:5123","bytes_in":10,"bytes_out":204800,"duration_ms":1500,"error":"","started_at":"2026-09-29T10:00:00Z"}],"next_before":""}`))
	}))
	mux.HandleFunc("POST /_tund/device/code", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		f.codeHash = b["token_hash"]
		f.mu.Unlock()
		w.Write([]byte(`{"device_code":"dc","user_code":"BCDF-GHJK","verification_url":"https://tund.test/device","verification_url_complete":"https://tund.test/device?code=BCDF-GHJK","interval":1,"expires_in":60}`))
	})
	mux.HandleFunc("POST /_tund/device/token", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.pending > 0 {
			f.pending--
			w.WriteHeader(428)
			w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		f.approved = f.codeHash
		w.Write([]byte(`{"status":"approved","account":"dev@example.com"}`))
	})
	return mux
}

type harness struct {
	t    *testing.T
	f    *fakeAPI
	srv  *Server
	sess *mcp.ClientSession
}

func newHarness(t *testing.T, token string, mutate func(*Options)) *harness {
	t.Helper()
	f := &fakeAPI{token: "tund_ok"}
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)
	opts := Options{Server: ts.URL, ServerSource: client.SourceConfig, Token: token, ConfigPath: filepath.Join(t.TempDir(), "tund.yml")}
	if mutate != nil {
		mutate(&opts)
	}
	srv := New(opts)
	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		srv.Run(ctx, st)
		close(done)
	}()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	sess, err := c.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sess.Close()
		cancel()
		<-done
	})
	return &harness{t: t, f: f, srv: srv, sess: sess}
}

func (h *harness) call(name string, args any) (*mcp.CallToolResult, map[string]any, string) {
	h.t.Helper()
	res, err := h.sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("%s: %v", name, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	var structured map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		json.Unmarshal(b, &structured)
	}
	return res, structured, text.String()
}

func TestToolList(t *testing.T) {
	h := newHarness(t, "tund_ok", nil)
	res, err := h.sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"whoami": true, "list_tunnels": true, "list_requests": true, "get_request": true, "list_connections": true,
		"login": false, "start_tunnel": false, "stop_tunnel": false, "replay_request": false}
	if len(res.Tools) != len(want) {
		t.Fatalf("got %d tools", len(res.Tools))
	}
	for _, tool := range res.Tools {
		ro, ok := want[tool.Name]
		if !ok {
			t.Errorf("unexpected tool %s", tool.Name)
			continue
		}
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != ro {
			t.Errorf("%s: readOnlyHint = %+v, want %v", tool.Name, tool.Annotations, ro)
		}
		if tool.Description == "" || tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Errorf("%s: missing description or schema", tool.Name)
		}
	}
	b, _ := json.Marshal(res.Tools)
	for _, s := range []string{`"target"`, `"max_body_chars"`, `"wait_seconds"`, "password", "public"} {
		if !strings.Contains(string(b), s) {
			t.Errorf("tool list lacks %s", s)
		}
	}
}

func TestWhoamiAndListing(t *testing.T) {
	h := newHarness(t, "tund_ok", nil)
	_, out, text := h.call("whoami", map[string]any{})
	if out["logged_in"] != true || out["account"].(map[string]any)["email"] != "dev@example.com" || !strings.Contains(text, "https://quiet-fox.tund.test") {
		t.Fatalf("whoami: %v / %s", out, text)
	}

	if !strings.Contains(text, "Teams: acme (admin)") || out["teams"].([]any)[0].(map[string]any)["slug"] != "acme" {
		t.Fatalf("whoami teams: %v / %s", out, text)
	}

	_, out, text = h.call("get_request", map[string]any{"id": "r4"})
	want := "Signed in as alice@example.com (Alice, groups: eng,admins, via https://idp.example.com)"
	if out["signed_in"] != want || !strings.Contains(text, want+"\n> GET /me") {
		t.Fatalf("signed in: %v / %s", out["signed_in"], text)
	}

	_, out, text = h.call("list_requests", map[string]any{"tunnel": "https://other.tund.test/", "status": "404", "method": "get"})
	reqs := out["requests"].([]any)
	if len(reqs) != 1 || reqs[0].(map[string]any)["id"] != "r3" || !strings.Contains(text, "id=r3") {
		t.Fatalf("list_requests: %v / %s", out, text)
	}
	if h.f.lastQuery != "hostname=other.tund.test&limit=20&method=GET&status=4xx" {
		t.Fatalf("query = %s", h.f.lastQuery)
	}
	if res, _, _ := h.call("list_requests", map[string]any{"status": "teapot"}); !res.IsError {
		t.Fatal("bad status must be a tool error")
	}

	_, out, text = h.call("get_request", map[string]any{"id": "r1", "max_body_chars": 10})
	reqBody := out["request"].(map[string]any)["body"].(map[string]any)
	respBody := out["response"].(map[string]any)["body"].(map[string]any)
	if reqBody["text"] != "xxxxxxxxxx" || !strings.Contains(reqBody["note"].(string), "first 10 of 50") ||
		respBody["text"] != nil || !strings.Contains(respBody["note"].(string), "binary body") {
		t.Fatalf("get_request bodies: %v", out)
	}
	if !strings.Contains(text, "> Content-Type: application/json") || !strings.Contains(text, "< 201 Created") {
		t.Fatalf("get_request text:\n%s", text)
	}
	if res, _, text := h.call("get_request", map[string]any{"id": "nope"}); !res.IsError || !strings.Contains(text, "no captured request") {
		t.Fatalf("get_request missing: %s", text)
	}

	_, out, text = h.call("list_tunnels", map[string]any{"all": true})
	if len(out["tunnels"].([]any)) != 0 || len(out["other"].([]any)) != 1 || !strings.Contains(text, "https://other.tund.test") {
		t.Fatalf("list_tunnels: %v / %s", out, text)
	}
	_, out, text = h.call("list_connections", map[string]any{"tunnel": "tcp://tund.test:20417"})
	if len(out["connections"].([]any)) != 1 || !strings.Contains(text, "203.0.113.5:5123 → tund.test:20417  ↑10 B ↓200.0 KB  1.50s") {
		t.Fatalf("list_connections: %v / %s", out, text)
	}
	if h.f.lastQuery != "address=tund.test%3A20417&limit=20" {
		t.Fatalf("connections query = %s", h.f.lastQuery)
	}
	h.call("list_connections", map[string]any{"tunnel": "3f1c-uuid"})
	if h.f.lastQuery != "limit=20&tunnel_id=3f1c-uuid" {
		t.Fatalf("connections query = %s", h.f.lastQuery)
	}

	_, out, _ = h.call("stop_tunnel", map[string]any{"url": "other.tund.test"})
	if out["stopped"] != true || len(h.f.stoppedIDs) != 1 || h.f.stoppedIDs[0] != "t-9" {
		t.Fatalf("stop_tunnel: %v %v", out, h.f.stoppedIDs)
	}
}

func TestReplayWaitsForCapture(t *testing.T) {
	h := newHarness(t, "tund_ok", nil)
	h.f.replayMiss = 2
	_, out, text := h.call("replay_request", map[string]any{"id": "r1", "method": "put", "headers": map[string]string{"X-Test": "1"}, "body": ""})
	if out["request_id"] != "r2" || out["request"] == nil || !strings.Contains(text, "Replayed r1 as r2") {
		t.Fatalf("replay: %v / %s", out, text)
	}
	if h.f.lastReplay["method"] != "PUT" || h.f.lastReplay["body"] != "" || h.f.lastReplay["headers"].(map[string]any)["X-Test"].([]any)[0] != "1" {
		t.Fatalf("replay body sent: %v", h.f.lastReplay)
	}

	old := replayWait
	replayWait = 300 * time.Millisecond
	defer func() { replayWait = old }()
	h.f.replayMiss = 100
	_, out, _ = h.call("replay_request", map[string]any{"id": "r1"})
	if out["request"] != nil || out["note"] == nil || len(h.f.lastReplay) != 0 {
		t.Fatalf("replay without capture: %v, sent %v", out, h.f.lastReplay)
	}
}

func TestNotLoggedInAndLogin(t *testing.T) {
	h := newHarness(t, "", nil)
	_, out, text := h.call("whoami", map[string]any{})
	if out["logged_in"] != false || !strings.Contains(text, "login") {
		t.Fatalf("whoami: %v %s", out, text)
	}
	if res, _, text := h.call("start_tunnel", map[string]any{"target": "3000"}); !res.IsError || !strings.Contains(text, "login tool") {
		t.Fatalf("start_tunnel without login: %s", text)
	}
	if res, _, _ := h.call("list_requests", map[string]any{}); !res.IsError {
		t.Fatal("list_requests without login must fail")
	}

	h.f.pending = 1
	_, out, text = h.call("login", map[string]any{})
	if out["status"] != "pending" || out["user_code"] != "BCDF-GHJK" || !strings.Contains(text, "https://tund.test/device?code=BCDF-GHJK") || out["browser_opened"] != nil {
		t.Fatalf("login: %v / %s", out, text)
	}
	// A second call reuses the pending login and waits for it.
	_, out, text = h.call("login", map[string]any{"wait_seconds": 10})
	if out["status"] != "approved" || out["account"] != "dev@example.com" {
		t.Fatalf("login wait: %v / %s", out, text)
	}
	cfg, err := client.LoadConfig(h.srv.opts.ConfigPath)
	if err != nil || !strings.HasPrefix(cfg.Authtoken, "tund_") || cfg.Server != h.srv.opts.Server {
		t.Fatalf("saved config: %+v %v", cfg, err)
	}
	_, out, _ = h.call("whoami", map[string]any{})
	if out["logged_in"] != true {
		t.Fatalf("whoami after login: %v", out)
	}
	_, out, _ = h.call("login", map[string]any{})
	if out["status"] != "already_logged_in" {
		t.Fatalf("login again: %v", out)
	}
}

func TestRevokedToken(t *testing.T) {
	h := newHarness(t, "tund_revoked", nil)
	_, out, text := h.call("whoami", map[string]any{})
	if out["logged_in"] != false || !strings.Contains(text, "login") {
		t.Fatalf("whoami with revoked token: %v %s", out, text)
	}
}

func TestStartTunnelGuards(t *testing.T) {
	h := newHarness(t, "tund_ok", func(o *Options) { o.AllowPorts = []int{3000} })
	for _, args := range []map[string]any{
		{"target": "4000"},
		{"target": "192.168.1.2:3000"},
		{"target": "ftp://localhost:3000"},
		{"target": "3000", "random": true, "subdomain": "x"},
		{"target": "3000", "password": "pw", "oidc": "acme/okta"},
		{"target": "3000", "oidc_allow": []string{"@x.com"}},
		{"target": "3000", "proto": "tcp", "subdomain": "x"},
		{"target": "3000", "proto": "tcp", "password": "pw"},
		{"target": "http://localhost:3000", "proto": "tcp"},
		{"target": "3000", "proto": "tls", "remote_port": 20000},
		{"target": "3000", "proto": "tls", "terminate_cert": "a.pem"},
		{"target": "3000", "proto": "http", "remote_port": 20000},
		{"target": "3000", "proto": "udp"},
		{"target": "3000", "allow_ips": []string{"not-an-ip"}},
	} {
		if res, _, text := h.call("start_tunnel", args); !res.IsError {
			t.Errorf("start_tunnel %v should fail, got %s", args, text)
		}
	}
}

func TestSignedIn(t *testing.T) {
	cases := []struct {
		h    map[string][]string
		want string
	}{
		{nil, ""},
		{map[string][]string{"Content-Type": {"text/html"}}, ""},
		{map[string][]string{"X-Tund-Auth": {"password"}}, "Visitor passed the password check (X-Tund-Auth: password)"},
		{map[string][]string{"x-tund-auth": {"oidc"}, "X-Tund-User-Username": {"bob"}}, "Signed in as bob"},
		{map[string][]string{"X-Tund-Auth": {"oidc"}, "X-Tund-User-Id": {"sub-1"}}, "Signed in as sub-1"},
		{map[string][]string{"X-Tund-Auth": {"oidc"}}, "Signed in as an unknown user"},
	}
	for _, c := range cases {
		if got := signedIn(c.h); got != c.want {
			t.Errorf("signedIn(%v) = %q, want %q", c.h, got, c.want)
		}
	}
}

func TestRequirePasswordRawTunnels(t *testing.T) {
	h := newHarness(t, "tund_ok", func(o *Options) { o.RequirePassword = true })
	res, _, text := h.call("start_tunnel", map[string]any{"target": "5432", "proto": "tcp"})
	if !res.IsError || !strings.Contains(text, "allow_ips") {
		t.Fatalf("tcp without allow_ips under --require-password: %s", text)
	}
}

func TestAddressOf(t *testing.T) {
	for in, want := range map[string]string{
		"tcp://tund.io:20417": "tund.io:20417", "tls://X.tund.io/": "x.tund.io", "tund.io:20417": "tund.io:20417", "https://a.tund.io": "a.tund.io",
	} {
		if got := addressOf(in); got != want {
			t.Errorf("addressOf(%q) = %q, want %q", in, got, want)
		}
	}
}
