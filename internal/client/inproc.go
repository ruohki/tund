package client

import (
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// streamListener hands the data streams of a tunnel served in this process
// (TunnelSpec.Handler) to its http.Server.
type streamListener struct {
	ch   chan net.Conn // unbuffered: deliver waits for Accept
	done chan struct{}
	once sync.Once
}

func newStreamListener() *streamListener {
	return &streamListener{ch: make(chan net.Conn), done: make(chan struct{})}
}

// Accept waits for the next stream; net.ErrClosed after Close.
func (l *streamListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close makes Accept and deliver give up; it can be called more than once.
func (l *streamListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *streamListener) Addr() net.Addr { return streamAddr{} }

// open reports whether the listener still takes streams.
func (l *streamListener) open() bool {
	select {
	case <-l.done:
		return false
	default:
		return true
	}
}

// deliver passes c to Accept; false once the listener is closed (c is then
// still the caller's to close).
func (l *streamListener) deliver(c net.Conn) bool {
	select {
	case l.ch <- c:
		return true
	case <-l.done:
		return false
	}
}

type streamAddr struct{}

func (streamAddr) Network() string { return "tund" }
func (streamAddr) String() string  { return "tund" }

// newInprocServer serves a Handler on the tunnel's data streams.
// IdleTimeout, ReadTimeout and WriteTimeout stay 0: the edge closes idle
// streams after 60s (IdleConnTimeout in server/proxy.go), and closing one
// first races with the edge reusing it for a PUT or POST, which then fails
// with a 502; read and write timeouts would cut large transfers.
func newInprocServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 30 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          log.New(io.Discard, "", 0), // never garble the terminal
	}
}

// startInproc starts the in-process server of every handler tunnel. Run
// calls it once, so the servers outlive reconnects; the returned func stops
// them. Neither ever closes a Handler: cmd/tund builds a new Client with the
// same Handler after a re-login.
func (c *Client) startInproc() (stop func()) {
	var stops []func()
	for _, t := range c.tunnels {
		if t.spec.Handler == nil {
			continue
		}
		l := newStreamListener()
		srv := newInprocServer(t.spec.Handler)
		served := make(chan struct{})
		go func() {
			defer close(served)
			_ = srv.Serve(l) // net.ErrClosed or http.ErrServerClosed
		}()
		c.mu.Lock()
		t.inproc = l
		c.mu.Unlock()
		stops = append(stops, func() {
			l.Close()
			srv.Close() // ends the streams it serves; handlers see their reads fail
			<-served
		})
	}
	return func() {
		for _, stop := range stops {
			stop()
		}
	}
}
