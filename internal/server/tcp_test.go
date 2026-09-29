package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestAllowList(t *testing.T) {
	allow, err := parseAllowList([]string{"203.0.113.0/24", "2001:db8::1", " 10.0.0.1 "})
	if err != nil {
		t.Fatal(err)
	}
	tun := &Tunnel{allow: allow}
	for addr, want := range map[string]bool{
		"203.0.113.7:5555":     true,
		"203.0.114.1:5555":     false,
		"[2001:db8::1]:443":    true,
		"[2001:db8::2]:443":    false,
		"10.0.0.1":             true,
		"[::ffff:10.0.0.1]:80": true, // v4-mapped
		"garbage":              false,
	} {
		if got := tun.ipAllowed(addr); got != want {
			t.Errorf("ipAllowed(%q) = %v, want %v", addr, got, want)
		}
	}
	if !(&Tunnel{}).ipAllowed("198.51.100.1:1") {
		t.Error("empty allow list must admit everyone")
	}
	if _, err := parseAllowList([]string{"300.1.1.1"}); err == nil {
		t.Error("invalid IP must be rejected")
	}
}

func selfSigned(t *testing.T, host string) tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// peekSNI must read the server name without consuming the handshake: a real
// TLS server fed the replayed bytes completes it.
func TestPeekSNIReplay(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	done := make(chan error, 1)
	go func() {
		c := tls.Client(clientConn, &tls.Config{ServerName: "app.example.test", InsecureSkipVerify: true})
		if err := c.Handshake(); err != nil {
			done <- err
			return
		}
		c.Write([]byte("ping"))
		c.Close()
		done <- nil
	}()
	sni, peeked, err := peekSNI(serverConn)
	if err != nil || sni != "app.example.test" || len(peeked) == 0 {
		t.Fatalf("peek: %q %d %v", sni, len(peeked), err)
	}
	srv := tls.Server(&prefixConn{Conn: serverConn, prefix: peeked}, &tls.Config{Certificates: []tls.Certificate{selfSigned(t, "app.example.test")}})
	if err := srv.Handshake(); err != nil {
		t.Fatalf("handshake after replay: %v", err)
	}
	b := make([]byte, 4)
	if _, err := io.ReadFull(srv, b); err != nil || string(b) != "ping" {
		t.Fatalf("read after replay: %q %v", b, err)
	}
	serverConn.Close()
	if err := <-done; err != nil {
		t.Fatalf("client: %v", err)
	}
}
