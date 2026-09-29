package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tund/internal/protocol"
)

// TCP tunnels and TLS passthrough (see docs/SPEC.md "TCP and TLS tunnels").

// tcpPorts is the pool of public ports for TCP tunnels.
type tcpPorts struct {
	from, to int
	mu       sync.Mutex
	used     map[int]*Tunnel
}

func newTCPPorts(from, to int) *tcpPorts {
	if from == 0 {
		return nil
	}
	return &tcpPorts{from: from, to: to, used: map[int]*Tunnel{}}
}

func (p *tcpPorts) inRange(port int) bool { return port >= p.from && port <= p.to }

func (s *Server) tcpHost() string {
	if s.cfg.TCPHost != "" {
		return s.cfg.TCPHost
	}
	return s.cfg.DashboardHost
}

func tcpAddress(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

// claimTCP picks and listens on a public port for a tcp bind. It is called
// with the account's hostname lock held.
func (s *Server) claimTCP(ctx context.Context, acct Account, b *protocol.Bind, t *Tunnel) (static bool, teamID string, err error) {
	p := s.tcp
	if p == nil {
		return false, "", bindError("TCP tunnels are not enabled on this server")
	}
	try := func(port int) (bool, string, error) {
		owner, team, reserved, err := s.store.TCPReservation(ctx, port)
		if err != nil {
			return false, "", err
		}
		if reserved {
			ok := owner == acct.UserID && team == ""
			if team != "" {
				ok, err = s.store.IsTeamMember(ctx, team, acct.UserID)
				if err != nil {
					return false, "", err
				}
			}
			if !ok {
				return false, "", bindError(fmt.Sprintf("port %d is reserved by another account", port))
			}
		}
		if err := s.listenTCP(t, port); err != nil {
			return false, "", err
		}
		return reserved, team, nil
	}

	if b.RemotePort != 0 && !b.Auto {
		if !p.inRange(b.RemotePort) {
			return false, "", bindError(fmt.Sprintf("port %d is outside this server's TCP range %d-%d", b.RemotePort, p.from, p.to))
		}
		return try(b.RemotePort)
	}
	// The account's (and its teams') reserved ports first, then the port this
	// client had last time, then a random free one.
	reserved, err := s.store.ReservedPorts(ctx, acct.UserID)
	if err != nil {
		return false, "", err
	}
	for _, port := range reserved {
		if static, team, err := try(port); err == nil {
			return static, team, nil
		}
	}
	if b.RemotePort != 0 && p.inRange(b.RemotePort) {
		if static, team, err := try(b.RemotePort); err == nil {
			return static, team, nil
		}
	}
	for range 50 {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(p.to-p.from+1)))
		port := p.from + int(n.Int64())
		if _, _, reserved, err := s.store.TCPReservation(ctx, port); err != nil || reserved {
			continue
		}
		if s.listenTCP(t, port) == nil {
			return false, "", nil
		}
	}
	return false, "", errors.New("no free TCP port available")
}

// listenTCP reserves port in the pool and opens the public listener.
func (s *Server) listenTCP(t *Tunnel, port int) error {
	p := s.tcp
	p.mu.Lock()
	if cur := p.used[port]; cur != nil {
		p.mu.Unlock()
		return bindError(fmt.Sprintf("port %d is already in use by another tunnel", port))
	}
	p.used[port] = t
	p.mu.Unlock()
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		p.release(port, t)
		return bindError(fmt.Sprintf("port %d is not available on this server", port))
	}
	t.listener = ln
	t.RemotePort = port
	t.Hostname = tcpAddress(s.tcpHost(), port)
	return nil
}

func (p *tcpPorts) release(port int, t *Tunnel) {
	p.mu.Lock()
	if p.used[port] == t {
		delete(p.used, port)
	}
	p.mu.Unlock()
}

// recheckTCP closes a tunnel on a team-reserved port whose owner left the
// team (or whose reservation moved away).
func (s *Server) recheckTCP(ctx context.Context, t *Tunnel) {
	if t.TeamID == "" {
		return
	}
	_, team, reserved, err := s.store.TCPReservation(ctx, t.RemotePort)
	if err != nil {
		return
	}
	member := false
	if reserved && team == t.TeamID {
		member, err = s.store.IsTeamMember(ctx, team, t.UserID)
		if err != nil {
			return
		}
	}
	if !member {
		t.session.unbind(t.BindID, fmt.Sprintf("you can no longer use port %d (it left your team, or you left the team)", t.RemotePort))
	}
}

// serveTCP accepts public connections for a tcp tunnel until its listener closes.
func (s *Server) serveTCP(t *Tunnel) {
	for {
		c, err := t.listener.Accept()
		if err != nil {
			return
		}
		go s.pipeConn(t, c, nil)
	}
}

// parseAllowList validates IPs/CIDRs of an allow list.
func parseAllowList(entries []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.Contains(e, "/") {
			a, err := netip.ParseAddr(e)
			if err != nil {
				return nil, bindError(fmt.Sprintf("invalid IP address %q in the allow list", e))
			}
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
			continue
		}
		pfx, err := netip.ParsePrefix(e)
		if err != nil {
			return nil, bindError(fmt.Sprintf("invalid CIDR %q in the allow list", e))
		}
		out = append(out, pfx.Masked())
	}
	return out, nil
}

// ipAllowed checks a visitor address ("ip" or "ip:port") against the allow list.
func (t *Tunnel) ipAllowed(addr string) bool {
	if len(t.allow) == 0 {
		return true
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range t.allow {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// countingWriter counts bytes copied in one direction.
type countingWriter struct {
	w io.Writer
	n atomic.Int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n.Add(int64(n))
	return n, err
}

// pipeConn forwards one TCP (or passthrough TLS) connection through the tunnel.
// prefix holds bytes already read from c (the peeked TLS ClientHello).
func (s *Server) pipeConn(t *Tunnel, c net.Conn, prefix []byte) {
	start := time.Now()
	remote := c.RemoteAddr().String()
	rec := &ConnRecord{
		ID: newUUID(), TunnelID: t.ID, UserID: t.UserID, Proto: t.Proto,
		Address: t.Hostname, RemoteAddr: clientIPString(remote), StartedAt: start,
	}
	defer func() {
		rec.Duration = time.Since(start)
		s.recorder.AddConn(rec)
		t.session.ctrl.Send(protocol.Message{Type: protocol.TypeConnection, ID: t.BindID, Conn: &protocol.ConnEvent{
			ConnectionID: rec.ID, RemoteAddr: rec.RemoteAddr, BytesIn: rec.BytesIn, BytesOut: rec.BytesOut,
			DurationMS: ms(rec.Duration), Error: rec.Error,
		}})
	}()
	defer c.Close()

	if !t.ipAllowed(remote) {
		rec.Error = "blocked by the IP allow list"
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	st, err := t.session.openStreamFor(ctx, t.BindID, remote)
	cancel()
	if err != nil {
		var le *protocol.LocalError
		if errors.As(err, &le) {
			rec.Error = le.Msg
		} else {
			rec.Error = err.Error()
		}
		return
	}
	defer st.Close()
	if t.meter != nil {
		t.meter.pendingConn.Add(1)
		if t.meter.blocked.Load() {
			rec.Error = errQuotaExceeded.Error()
			return
		}
		st = &meteredConn{Conn: st, srv: s, m: t.meter}
	}

	in := &countingWriter{w: st}
	out := &countingWriter{w: c}
	if len(prefix) > 0 {
		if _, err := in.Write(prefix); err != nil {
			rec.Error = err.Error()
			return
		}
	}
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(in, c)
		closeWrite(st)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(out, st)
		closeWrite(c)
		done <- struct{}{}
	}()
	<-done
	// Give the other direction a moment to finish after a half-close.
	select {
	case <-done:
	case <-time.After(30 * time.Second):
	}
	rec.BytesIn, rec.BytesOut = in.n.Load(), out.n.Load()
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
		return
	}
	c.Close()
}

func clientIPString(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}
