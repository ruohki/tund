package client

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"tund/internal/protocol"
)

func bindFor(t *testing.T, tn *tunnel) protocol.Bind {
	t.Helper()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := &Client{}
	go func() { _ = c.sendBind(protocol.NewControl(a), tn) }()
	m, err := protocol.NewControl(b).Recv()
	if err != nil || m.Type != protocol.TypeBind || m.Bind == nil || m.ID != tn.spec.Name {
		t.Fatalf("bind message %+v, %v", m, err)
	}
	return *m.Bind
}

func TestBindMessage(t *testing.T) {
	// Remembered label is only a preference.
	b := bindFor(t, &tunnel{spec: TunnelSpec{Name: "web", LocalAddr: "http://localhost:3000"}, label: "brave-otter-1"})
	if b.Subdomain != "brave-otter-1" || !b.Auto || b.Random || b.Pin {
		t.Errorf("remembered: %+v", b)
	}
	// Nothing remembered: no subdomain, no auto.
	b = bindFor(t, &tunnel{spec: TunnelSpec{Name: "web", LocalAddr: "http://localhost:3000", Random: true, Pin: true}})
	if b.Subdomain != "" || b.Auto || !b.Random || !b.Pin {
		t.Errorf("random+pin: %+v", b)
	}
	// Explicit subdomain is never auto, and ignores any label.
	b = bindFor(t, &tunnel{spec: TunnelSpec{Name: "web", LocalAddr: "http://localhost:3000", Subdomain: "myapp", Pin: true}, label: "old"})
	if b.Subdomain != "myapp" || b.Auto || !b.Pin {
		t.Errorf("explicit: %+v", b)
	}
}

func TestHandlerBindMessage(t *testing.T) {
	// The server stores local_addr and shows it to admins, in logs and on the
	// visitors' 502 page: a Handler tunnel sends its label, never Display.
	tn := &tunnel{spec: handlerSpec(http.NotFoundHandler()), label: "calm-owl-7"}
	a, b := net.Pipe()
	defer b.Close()
	go func() {
		_ = (&Client{}).sendBind(protocol.NewControl(a), tn)
		a.Close()
	}()
	raw, _ := io.ReadAll(b)
	var m protocol.Message
	if err := json.Unmarshal(raw, &m); err != nil || m.Bind == nil {
		t.Fatalf("bind %s: %v", raw, err)
	}
	if bind := m.Bind; bind.LocalAddr != "http://file-share" || bind.Auth == nil || bind.Auth.Mode != protocol.AuthPassword ||
		bind.Subdomain != "calm-owl-7" || !bind.Auto {
		t.Errorf("bind = %s", raw)
	}
	if strings.Contains(string(raw), "/home/alice") {
		t.Errorf("bind carries the local path: %s", raw)
	}
}

func TestUnprotectedHandler(t *testing.T) {
	files := &tunnel{spec: handlerSpec(http.NotFoundHandler())}
	web := &tunnel{spec: TunnelSpec{Name: "web", LocalAddr: "http://localhost:3000"}}
	for _, c := range []struct {
		t    *tunnel
		mode string
		want bool
	}{
		{files, protocol.AuthPassword, false},
		{files, protocol.AuthOIDC, false}, // e.g. a team's required single sign-on replaced the password
		{files, protocol.AuthNone, true},
		{files, "", true},
		{files, "basic", true},
		{web, protocol.AuthNone, false},
		{web, "", false},
	} {
		if got := unprotectedHandler(c.t, protocol.Message{Type: protocol.TypeBound, AuthMode: c.mode}); got != c.want {
			t.Errorf("unprotectedHandler(%s, %q) = %v", c.t.spec.Name, c.mode, got)
		}
	}
}

func TestRefuseUnprotected(t *testing.T) {
	tn := &tunnel{spec: handlerSpec(http.NotFoundHandler()), status: statusPending}
	c := &Client{tunnels: []*tunnel{tn}, byName: map[string]*tunnel{"files": tn}}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	view := make(chan tunnelView, 1)
	go func() { view <- c.refuseUnprotected(protocol.NewControl(a), tn, "https://calm-owl-7.tund.example") }()
	m, err := protocol.NewControl(b).Recv()
	if err != nil || m.Type != protocol.TypeUnbind || m.ID != "files" {
		t.Fatalf("message %+v, %v", m, err)
	}
	v := <-view
	want := "the server did not protect https://calm-owl-7.tund.example with a password or single sign-on; refusing to serve files"
	if !v.Failed || v.Err != want || v.Local != "/home/alice/share" {
		t.Errorf("view = %+v", v)
	}
	if online, pending, failed, _ := c.counts(); online+pending != 0 || failed != 1 {
		t.Errorf("counts: %d online, %d pending, %d failed", online, pending, failed)
	}
}

func TestStateKey(t *testing.T) {
	c := &Client{opts: Options{Server: "https://tund.example.com"}}
	h := http.NotFoundHandler()
	for _, tc := range []struct {
		spec TunnelSpec
		want string
	}{
		{TunnelSpec{LocalAddr: "http://localhost:3000"}, "https://tund.example.com http://localhost:3000"},
		{TunnelSpec{LocalAddr: "http://localhost:3000", Display: "ignored"}, "https://tund.example.com http://localhost:3000"},
		{TunnelSpec{Proto: "tcp", LocalAddr: "localhost:5432"}, "https://tund.example.com tcp localhost:5432"},
		// Every share has the same label: each path keeps its own hostname.
		{TunnelSpec{LocalAddr: "http://file-share", Display: "~/share", Handler: h}, "https://tund.example.com handler ~/share"},
		{TunnelSpec{LocalAddr: "http://file-share", Display: "~/inbox", Handler: h}, "https://tund.example.com handler ~/inbox"},
		{TunnelSpec{LocalAddr: "http://file-share", Handler: h}, "https://tund.example.com handler http://file-share"},
	} {
		if got := c.stateKey(&tunnel{spec: tc.spec}); got != tc.want {
			t.Errorf("stateKey(%+v) = %q, want %q", tc.spec, got, tc.want)
		}
	}
}

func TestSpecPinRandom(t *testing.T) {
	s, err := (&TunnelConfig{Addr: "3000", Pin: true, Random: true}).Spec("p")
	if err != nil || !s.Pin || !s.Random {
		t.Fatalf("spec = %+v, %v", s, err)
	}
	if _, err := (&TunnelConfig{Addr: "3000", Random: true, Subdomain: "x"}).Spec("p"); err == nil {
		t.Fatal("random+subdomain must fail")
	}
	if _, err := (&TunnelConfig{Addr: "3000", Random: true, Domain: "x.example.com"}).Spec("p"); err == nil {
		t.Fatal("random+domain must fail")
	}
}
