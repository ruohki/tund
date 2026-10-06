package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"tund/internal/protocol"
)

// echoHandler answers PUTs with the size and SHA-256 of the body, and
// everything else with the visitor's X-Tund-User-Email.
type echoHandler struct{ closed atomic.Bool }

func (h *echoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		sum := sha256.New()
		n, err := io.Copy(sum, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, "%d %x", n, sum.Sum(nil))
		return
	}
	io.WriteString(w, r.Header.Get("X-Tund-User-Email"))
}

// Close would end the handler for good; the client must never call it.
func (h *echoHandler) Close() error {
	h.closed.Store(true)
	return nil
}

func handlerSpec(h http.Handler) TunnelSpec {
	return TunnelSpec{Name: "files", LocalAddr: "http://file-share", Display: "/home/alice/share", Handler: h,
		Auth: &protocol.Auth{Mode: protocol.AuthPassword, Password: "correct-horse"}}
}

// streamRefused checks that the client turned a stream away with msg, and
// that the text (which reaches the edge) names no local path.
func streamRefused(t *testing.T, err error, msg string) {
	t.Helper()
	var le *protocol.LocalError
	if !errors.As(err, &le) || !strings.Contains(le.Msg, msg) || strings.Contains(le.Msg, "/home/alice") {
		t.Errorf("stream error = %v, want a LocalError with %q", err, msg)
	}
}

// TestHandlerOverYamux runs an edge-like Transport (server/proxy.go) over
// yamux streams that handleStream hands to the in-process server.
func TestHandlerOverYamux(t *testing.T) {
	h := &echoHandler{}
	c, err := New(Options{Server: "https://tund.example.com", Authtoken: "tund_x", Tunnels: []TunnelSpec{handlerSpec(h)}, Events: func(Event) {}})
	if err != nil {
		t.Fatal(err)
	}
	tn := c.byName["files"]
	if tn.target != (localTarget{}) {
		t.Fatalf("a Handler tunnel got a dial target: %+v", tn.target)
	}
	setStatus := func(s tunnelStatus) {
		c.mu.Lock()
		tn.status = s
		c.mu.Unlock()
	}
	stop := c.startInproc()
	setStatus(statusOnline)

	a, b := net.Pipe()
	edge, _ := yamux.Server(a, protocol.YamuxConfig())
	cli, _ := yamux.Client(b, protocol.YamuxConfig())
	defer edge.Close()
	defer cli.Close()
	go c.acceptStreams(cli)

	// openStream is the edge's dialStream (server/session.go).
	openStream := func(hdr protocol.StreamHeader) (net.Conn, error) {
		st, err := edge.OpenStream()
		if err != nil {
			return nil, err
		}
		st.SetDeadline(time.Now().Add(15 * time.Second))
		if err := protocol.WriteStreamHeader(st, hdr); err != nil {
			st.Close()
			return nil, err
		}
		if err := protocol.ReadStreamStatus(st); err != nil {
			st.Close()
			return nil, err
		}
		st.SetDeadline(time.Time{})
		return st, nil
	}
	var dials atomic.Int32
	tr := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return openStream(protocol.StreamHeader{Tunnel: "files"})
		},
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       60 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
	}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	do := func(req *http.Request) (string, error) {
		resp, err := hc.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err == nil && resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("status %d", resp.StatusCode)
		}
		return string(body), err
	}
	get := func(email string) (string, error) {
		req, _ := http.NewRequest(http.MethodGet, "http://files.tund.example/", nil)
		if email != "" {
			req.Header.Set("X-Tund-User-Email", email)
		}
		return do(req)
	}

	// One stream carries the requests of several visitors, one after the
	// other: each sees its own identity, never the one before.
	for _, email := range []string{"alice@example.com", "bob@example.com", "", "carol@example.com"} {
		if got, err := get(email); err != nil || got != email {
			t.Errorf("GET as %q saw %q, %v", email, got, err)
		}
	}
	if n := dials.Load(); n != 1 {
		t.Errorf("%d streams for sequential requests, want 1 reused stream", n)
	}

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			email := fmt.Sprintf("visitor%d@example.com", i)
			if got, err := get(email); err != nil || got != email {
				t.Errorf("parallel GET as %q saw %q, %v", email, got, err)
			}
		}()
	}
	wg.Wait()

	// A visitor's upload as the edge forwards it: Expect: 100-continue and a
	// body that can't be rewound, so the PUT can't be retried elsewhere.
	data := make([]byte, 8<<20)
	for i := range data {
		data[i] = byte(i * 31)
	}
	var got100 atomic.Bool
	trace := &httptrace.ClientTrace{Got100Continue: func() { got100.Store(true) }}
	req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), http.MethodPut,
		"http://files.tund.example/inbox/big.bin", io.NopCloser(bytes.NewReader(data)))
	req.ContentLength = int64(len(data))
	req.Header.Set("Expect", "100-continue")
	if got, err := do(req); err != nil || got != fmt.Sprintf("%d %x", len(data), sha256.Sum256(data)) {
		t.Errorf("PUT stored %q, %v", got, err)
	}
	if !got100.Load() {
		t.Error("the PUT got no 100 Continue")
	}
	if got, err := get("dave@example.com"); err != nil || got != "dave@example.com" {
		t.Errorf("GET after PUT saw %q, %v", got, err)
	}

	_, err = openStream(protocol.StreamHeader{Tunnel: "files", Route: 1})
	streamRefused(t, err, "has no route 1")
	_, err = openStream(protocol.StreamHeader{Tunnel: "nope"})
	streamRefused(t, err, "not active")
	setStatus(statusFailed)
	_, err = openStream(protocol.StreamHeader{Tunnel: "files"})
	streamRefused(t, err, "not active")
	setStatus(statusOnline)

	stop()
	_, err = openStream(protocol.StreamHeader{Tunnel: "files"})
	streamRefused(t, err, "the file share is shutting down")
	if h.closed.Load() {
		t.Error("the client closed the Handler")
	}
}

// TestHandlerTunnelSession runs a Client against a fake edge. The first bind
// is protected and a request goes through Run to the Handler; then the edge
// drops the session, and the bind after the reconnect comes back without
// protection, so the client unbinds and gives up.
func TestHandlerTunnelSession(t *testing.T) {
	modes := make(chan string, 2)
	modes <- protocol.AuthPassword
	modes <- protocol.AuthNone
	served := make(chan string, 1)
	unbound := make(chan protocol.Message, 1)
	get := func(sess *yamux.Session, tunnel string) string {
		st, err := sess.OpenStream()
		if err != nil {
			return err.Error()
		}
		defer st.Close()
		if err := protocol.WriteStreamHeader(st, protocol.StreamHeader{Tunnel: tunnel}); err != nil {
			return err.Error()
		}
		if err := protocol.ReadStreamStatus(st); err != nil {
			return err.Error()
		}
		req, _ := http.NewRequest(http.MethodGet, "http://calm-owl-7.tund.example/", nil)
		req.Header.Set("X-Tund-User-Email", "alice@example.com")
		if err := req.Write(st); err != nil {
			return err.Error()
		}
		resp, err := http.ReadResponse(bufio.NewReader(st), req)
		if err != nil {
			return err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return
		}
		sess, err := yamux.Server(websocket.NetConn(context.Background(), ws, websocket.MessageBinary), protocol.YamuxConfig())
		if err != nil {
			return
		}
		defer sess.Close()
		st, err := sess.AcceptStream()
		if err != nil {
			return
		}
		ctl := protocol.NewControl(st)
		_ = ctl.Send(protocol.Message{Type: protocol.TypeWelcome, ServerVersion: "dev"})
		m, err := ctl.Recv()
		if err != nil || m.Type != protocol.TypeBind {
			return
		}
		mode := <-modes
		_ = ctl.Send(protocol.Message{Type: protocol.TypeBound, ID: m.ID, URL: "https://calm-owl-7.tund.example", AuthMode: mode})
		if mode == protocol.AuthNone {
			m, _ = ctl.Recv()
			unbound <- m
			_, _ = ctl.Recv() // until the client hangs up
			return
		}
		served <- get(sess, m.ID) // then drop the session
	}))
	defer edge.Close()

	h := &echoHandler{}
	var mu sync.Mutex
	var failed []string
	c, err := New(Options{Server: edge.URL, Authtoken: "tund_x", Tunnels: []TunnelSpec{handlerSpec(h)}, Events: func(e Event) {
		if e.Kind == EventFailed {
			mu.Lock()
			failed = append(failed, e.Error)
			mu.Unlock()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := c.Run(ctx); err == nil || !strings.Contains(err.Error(), "no tunnel could be started") {
		t.Errorf("Run = %v", err)
	}
	select {
	case got := <-served:
		if got != "alice@example.com" {
			t.Errorf("request through the protected tunnel: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Error("no request went through the protected tunnel")
	}
	select {
	case m := <-unbound:
		if m.Type != protocol.TypeUnbind || m.ID != "files" {
			t.Errorf("after an unprotected bound: %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Error("no unbind after an unprotected bound")
	}
	mu.Lock()
	defer mu.Unlock()
	want := "the server did not protect https://calm-owl-7.tund.example with a password or single sign-on; refusing to serve files"
	if len(failed) != 1 || failed[0] != want {
		t.Errorf("failures = %q", failed)
	}
	if h.closed.Load() {
		t.Error("the client closed the Handler")
	}
}

func TestRunStopsInprocServers(t *testing.T) {
	h := &echoHandler{}
	c, err := New(Options{Server: "http://127.0.0.1:1", Authtoken: "tund_x", Tunnels: []TunnelSpec{handlerSpec(h)}, Events: func(Event) {}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	l := c.byName["files"].inproc
	c.mu.Unlock()
	if l == nil || l.open() {
		t.Fatalf("listener after Run = %v, want started and closed", l)
	}
	if h.closed.Load() {
		t.Error("Run closed the Handler")
	}
}

func TestInprocServerConfig(t *testing.T) {
	srv := newInprocServer(http.NotFoundHandler())
	// The edge reuses idle streams for up to 60s: closing them first turns
	// reused PUTs into 502s. Read/write timeouts would cut large transfers.
	if srv.IdleTimeout != 0 || srv.ReadTimeout != 0 || srv.WriteTimeout != 0 {
		t.Errorf("timeouts: idle %v, read %v, write %v; want none", srv.IdleTimeout, srv.ReadTimeout, srv.WriteTimeout)
	}
	if srv.ReadHeaderTimeout != 30*time.Second || srv.MaxHeaderBytes != 64<<10 || srv.ErrorLog == nil {
		t.Errorf("server = %+v", srv)
	}
}

func TestStreamListener(t *testing.T) {
	l := newStreamListener()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() {
		if !l.deliver(a) {
			t.Error("deliver to an open listener failed")
		}
	}()
	if c, err := l.Accept(); err != nil || c != a {
		t.Fatalf("Accept = %v, %v", c, err)
	}
	if !l.open() || l.Addr().Network() != "tund" {
		t.Fatalf("open %v, addr %v", l.open(), l.Addr())
	}
	l.Close()
	l.Close()
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Accept after Close = %v", err)
	}
	if l.open() || l.deliver(b) {
		t.Error("closed listener still takes streams")
	}
}

func TestTargetForHandler(t *testing.T) {
	// A second guard behind New: a Handler's LocalAddr is never dialed.
	if lt, err := targetFor(handlerSpec(http.NotFoundHandler())); err != nil || lt != (localTarget{}) {
		t.Fatalf("targetFor = %+v, %v", lt, err)
	}
}
