package server

import (
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestExplainProxyErrorNextDevOrigins(t *testing.T) {
	// The Next.js dev server answers a blocked WebSocket upgrade with raw bytes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			c.Read(buf)
			c.Write([]byte("Unauthorized"))
			c.Close()
		}
	}()

	roundTrip := func(path string) (*http.Request, error) {
		r, _ := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+path, nil)
		r.Host = "gerb.example.com:443"
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		_, err := (&http.Transport{}).RoundTrip(r)
		if err == nil {
			t.Fatal("expected a transport error")
		}
		return r, err
	}

	r, err := roundTrip("/_next/hmr?id=x")
	if got := explainProxyError(err, r); !strings.Contains(got, `add "gerb.example.com" to allowedDevOrigins`) {
		t.Fatalf("got %q", got)
	}
	r, err = roundTrip("/socket")
	if got := explainProxyError(err, r); got != err.Error() {
		t.Fatalf("other paths keep the transport error, got %q", got)
	}
}
