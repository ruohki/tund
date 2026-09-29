package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient(t *testing.T) {
	var lastQuery string
	var lastBody map[string]any
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tund_ok" {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":"invalid authtoken"}`))
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /_tund/api/v1/me", auth(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"account":{"id":"u1","email":"a@b.c","name":"A","is_admin":true},"server":{"base_domain":"tund.io","dashboard_url":"https://tund.io","version":"1.2"},"limits":{"tunnels":5,"pinned":3,"domains":0},"static_hostnames":[{"hostname":"x.tund.io","url":"https://x.tund.io","default":true}],"teams":[{"slug":"acme","name":"Acme","role":"owner"}]}`))
	}))
	mux.HandleFunc("GET /_tund/api/v1/tunnels", auth(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tunnels":[{"id":"t1","name":"web","hostname":"x.tund.io","url":"https://x.tund.io","local_addr":"http://localhost:3000","auth_mode":"none","static":true,"started_at":"2026-09-29T10:00:00Z","client":{"hostname":"mbp","os":"darwin/arm64","version":"1.0"},"proto":"tcp","remote_port":20417}]}`))
	}))
	mux.HandleFunc("POST /_tund/api/v1/tunnels/{id}/stop", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "t1" {
			w.WriteHeader(404)
			w.Write([]byte(`{"error":"tunnel not found"}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	mux.HandleFunc("GET /_tund/api/v1/requests", auth(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.RawQuery
		w.Write([]byte(`{"requests":[{"id":"r1","hostname":"x.tund.io","method":"GET","path":"/a?b=1","status":200,"duration_ms":1.5,"started_at":"2026-09-29T10:00:00Z"}],"next_before":"2026-09-29T10:00:00Z"}`))
	}))
	mux.HandleFunc("GET /_tund/api/v1/requests/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"r1","method":"POST","path":"/p","status":201,"proto":"HTTP/1.1","request":{"headers":{"Content-Type":["application/json"]},"body":{"size":7,"content_type":"application/json","text":"{\"a\":1}"}},"response":{"headers":{},"body":{"size":3,"base64":"AAEC"}}}`))
	}))
	mux.HandleFunc("POST /_tund/api/v1/requests/{id}/replay", auth(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody = nil
		json.Unmarshal(b, &lastBody)
		w.Write([]byte(`{"request_id":"r2","status":200}`))
	}))
	mux.HandleFunc("GET /_tund/api/v1/connections", auth(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.RawQuery
		w.Write([]byte(`{"connections":[{"id":"c1","tunnel_id":"t2","proto":"tcp","address":"tund.io:20417","remote_addr":"203.0.113.5:5123","bytes_in":10,"bytes_out":2048,"duration_ms":1500,"error":"","started_at":"2026-09-29T10:00:00Z"}],"next_before":""}`))
	}))
	ts := httptest.NewServer(mux)
	defer ts.Close()
	ctx := context.Background()
	c := New(ts.URL+"/", "tund_ok")

	me, err := c.Me(ctx)
	if err != nil || me.Account.Email != "a@b.c" || !me.Account.IsAdmin || me.Limits.Pinned != 3 || len(me.StaticHostnames) != 1 || !me.StaticHostnames[0].Default || me.Server.BaseDomain != "tund.io" || len(me.Teams) != 1 || me.Teams[0].Role != "owner" {
		t.Fatalf("Me = %+v, %v", me, err)
	}
	tun, err := c.Tunnels(ctx)
	if err != nil || len(tun) != 1 || tun[0].Client.OS != "darwin/arm64" || !tun[0].Static || tun[0].Proto != "tcp" || tun[0].RemotePort != 20417 {
		t.Fatalf("Tunnels = %+v, %v", tun, err)
	}
	if err := c.StopTunnel(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	if err := c.StopTunnel(ctx, "nope"); StatusOf(err) != 404 || err.Error() != "tund API: tunnel not found" {
		t.Fatalf("StopTunnel(nope) = %v", err)
	}
	list, err := c.Requests(ctx, RequestFilter{Hostname: "x.tund.io", Status: "4xx", Path: "/a b", Limit: 10})
	if err != nil || len(list.Requests) != 1 || list.Requests[0].DurationMS != 1.5 || list.NextBefore == "" {
		t.Fatalf("Requests = %+v, %v", list, err)
	}
	if lastQuery != "hostname=x.tund.io&limit=10&path=%2Fa+b&status=4xx" {
		t.Fatalf("query = %q", lastQuery)
	}
	req, err := c.Request(ctx, "r1")
	if err != nil || req.Status != 201 || req.Request.Body.Text != `{"a":1}` || req.Response.Body.Base64 != "AAEC" || req.Request.Headers["Content-Type"][0] != "application/json" {
		t.Fatalf("Request = %+v, %v", req, err)
	}
	if res, err := c.Replay(ctx, "r1", nil); err != nil || res.RequestID != "r2" || len(lastBody) != 0 {
		t.Fatalf("Replay = %+v, %v, body %v", res, err, lastBody)
	}
	body := "x"
	if _, err := c.Replay(ctx, "r1", &ReplayOptions{Method: "PUT", Body: &body}); err != nil || lastBody["method"] != "PUT" || lastBody["body"] != "x" {
		t.Fatalf("Replay override body %v, %v", lastBody, err)
	}
	conns, err := c.ListConnections(ctx, ConnectionFilter{Address: "tund.io:20417", Limit: 5})
	if err != nil || len(conns.Connections) != 1 || conns.Connections[0].BytesOut != 2048 || conns.Connections[0].Proto != "tcp" {
		t.Fatalf("ListConnections = %+v, %v", conns, err)
	}
	if lastQuery != "address=tund.io%3A20417&limit=5" {
		t.Fatalf("connections query = %q", lastQuery)
	}
	if _, err := New(ts.URL, "bad").Me(ctx); StatusOf(err) != 401 {
		t.Fatalf("bad token: %v", err)
	}
}
