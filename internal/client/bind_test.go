package client

import (
	"net"
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
