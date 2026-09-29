package server

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"tund/internal/protocol"
)

// sniRouter sits in front of the HTTPS server. It peeks at every ClientHello:
// connections for TLS-passthrough tunnels are piped through the tunnel
// untouched, everything else is handed to the HTTPS server with the peeked
// bytes replayed.
type sniRouter struct {
	net.Listener
	srv    *Server
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newSNIRouter(ln net.Listener, s *Server) *sniRouter {
	r := &sniRouter{Listener: ln, srv: s, conns: make(chan net.Conn), closed: make(chan struct{})}
	go r.run()
	return r
}

func (r *sniRouter) run() {
	for {
		c, err := r.Listener.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			r.Close()
			return
		}
		go r.route(c)
	}
}

func (r *sniRouter) route(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	sni, peeked, err := peekSNI(c)
	c.SetReadDeadline(time.Time{})
	if err == nil && sni != "" {
		// Blocked hostnames never pass through; the HTTPS server answers them
		// with the "blocked" page (where it has a certificate).
		_, blocked := r.srv.blockedReason(normalizeHost(sni))
		if t := r.srv.reg.Lookup(normalizeHost(sni)); !blocked && t != nil && t.Proto == protocol.ProtoTLS {
			r.srv.pipeConn(t, c, peeked)
			return
		}
	}
	select {
	case r.conns <- &prefixConn{Conn: c, prefix: peeked}:
	case <-r.closed:
		c.Close()
	}
}

// Accept hands non-passthrough connections to the HTTPS server.
func (r *sniRouter) Accept() (net.Conn, error) {
	select {
	case c := <-r.conns:
		return c, nil
	case <-r.closed:
		return nil, net.ErrClosed
	}
}

func (r *sniRouter) Close() error {
	r.once.Do(func() { close(r.closed) })
	return r.Listener.Close()
}

var errPeekDone = errors.New("peek done")

// peekSNI reads the ClientHello and returns its server name plus every byte
// read, which must be replayed to whoever handles the connection.
func peekSNI(c net.Conn) (string, []byte, error) {
	rec := &recordingConn{r: c}
	var sni string
	err := tls.Server(rec, &tls.Config{
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			sni = h.ServerName
			return nil, errPeekDone
		},
	}).Handshake()
	if sni == "" && !errors.Is(err, errPeekDone) {
		// Not TLS, or a broken hello: the HTTPS server will reject it.
		return "", rec.buf.Bytes(), err
	}
	return sni, rec.buf.Bytes(), nil
}

// recordingConn is a read-only net.Conn that keeps a copy of what was read
// and swallows writes (the aborted handshake's alert must not reach the client).
type recordingConn struct {
	r   io.Reader
	buf bytes.Buffer
}

func (c *recordingConn) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.buf.Write(b[:n])
	return n, err
}
func (c *recordingConn) Write(b []byte) (int, error)        { return len(b), nil }
func (c *recordingConn) Close() error                       { return nil }
func (c *recordingConn) LocalAddr() net.Addr                { return nil }
func (c *recordingConn) RemoteAddr() net.Addr               { return nil }
func (c *recordingConn) SetDeadline(t time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(t time.Time) error { return nil }

// prefixConn replays already-read bytes before reading from the connection.
type prefixConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixConn) Read(b []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(b, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(b)
}

// CloseWrite lets pipeConn half-close replayed connections too.
func (c *prefixConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Conn.Close()
}
