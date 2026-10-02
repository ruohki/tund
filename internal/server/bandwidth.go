package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"tund/internal/protocol"
)

// Bandwidth limits per account (docs/SPEC.md "Bandwidth limits"): a
// throughput cap per direction and a monthly transfer quota.

const (
	meterBurst      = 256 << 10 // bytes; also the largest chunk passed to a limiter
	meterFlushEvery = 15 * time.Second
	meterRefresh    = 60 * time.Second
)

var errQuotaExceeded = errors.New("monthly transfer quota used up")

type accountMeter struct {
	userID string

	mu         sync.Mutex
	in, out    *rate.Limiter // nil = unlimited
	kbps       int
	quotaBytes int64 // 0 = unlimited
	monthUsed  int64 // bytes this month as known from the database + flushed by us
	periodEnd  time.Time

	pendingIn, pendingOut, pendingReq, pendingConn atomic.Int64
	blocked                                        atomic.Bool
	lastUsed                                       atomic.Int64 // unix seconds, for idle eviction
}

type meters struct {
	mu sync.Mutex
	m  map[string]*accountMeter
}

func monthBounds(now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

// meterFor returns the account's meter, loading limits and usage on first use.
func (s *Server) meterFor(ctx context.Context, userID string) (*accountMeter, error) {
	s.meters.mu.Lock()
	m := s.meters.m[userID]
	s.meters.mu.Unlock()
	if m != nil {
		m.lastUsed.Store(time.Now().Unix())
		return m, nil
	}
	m = &accountMeter{userID: userID}
	if err := s.refreshMeter(ctx, m); err != nil {
		return nil, err
	}
	m.lastUsed.Store(time.Now().Unix())
	s.meters.mu.Lock()
	defer s.meters.mu.Unlock()
	if cur := s.meters.m[userID]; cur != nil {
		return cur, nil
	}
	s.meters.m[userID] = m
	return m, nil
}

// refreshMeter reloads the account's limits and this month's usage.
func (s *Server) refreshMeter(ctx context.Context, m *accountMeter) error {
	plan, lim, err := s.accountPlan(ctx, m.userID)
	if err != nil {
		return err
	}
	start, end := monthBounds(time.Now())
	used, err := s.store.MonthUsage(ctx, m.userID, start)
	if err != nil {
		return err
	}
	kbps, quotaGB := plan.BandwidthKbps, plan.TransferGB
	if lim.IsAdmin {
		kbps, quotaGB = 0, 0
	}
	if lim.BandwidthKbps != nil {
		kbps = *lim.BandwidthKbps
	}
	if lim.TransferQuotaGB != nil {
		quotaGB = *lim.TransferQuotaGB
	}

	m.mu.Lock()
	if kbps != m.kbps || (kbps > 0 && m.in == nil) {
		m.kbps = kbps
		if kbps > 0 {
			bps := rate.Limit(float64(kbps) * 1000 / 8)
			m.in, m.out = rate.NewLimiter(bps, meterBurst), rate.NewLimiter(bps, meterBurst)
		} else {
			m.in, m.out = nil, nil
		}
	}
	m.quotaBytes = int64(quotaGB) * 1_000_000_000
	m.monthUsed = used.BytesIn + used.BytesOut
	m.periodEnd = end
	m.mu.Unlock()
	s.checkQuota(m)
	return nil
}

func (m *accountMeter) limiters() (in, out *rate.Limiter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.in, m.out
}

func (m *accountMeter) used() int64 {
	m.mu.Lock()
	base := m.monthUsed
	m.mu.Unlock()
	return base + m.pendingIn.Load() + m.pendingOut.Load()
}

// checkQuota updates the blocked flag and tells the account's clients once.
func (s *Server) checkQuota(m *accountMeter) {
	m.mu.Lock()
	quota, end := m.quotaBytes, m.periodEnd
	m.mu.Unlock()
	over := quota > 0 && m.used() >= quota
	if over == m.blocked.Load() {
		return
	}
	m.blocked.Store(over)
	if !over {
		logf("transfer quota block lifted for %s", m.userID)
		return
	}
	msg := quotaMessage(quota, end)
	logf("account %s: %s", m.userID, msg)
	for _, as := range s.reg.Sessions() {
		if as.Account.UserID == m.userID {
			as.ctrl.Send(protocol.Message{Type: protocol.TypeNotice, Error: msg})
		}
	}
}

func quotaMessage(quota int64, periodEnd time.Time) string {
	return fmt.Sprintf("monthly transfer quota of %s used up; traffic is blocked until %s (UTC)",
		humanBytes(quota), periodEnd.Format("2 Jan 2006"))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1f kB", float64(n)/1e3)
	}
	return fmt.Sprintf("%d B", n)
}

func (s *Server) addUsage(m *accountMeter, in, out int64) {
	s.traffic.Add(in + out)
	if in > 0 {
		m.pendingIn.Add(in)
	}
	if out > 0 {
		m.pendingOut.Add(out)
	}
	m.mu.Lock()
	quota := m.quotaBytes
	m.mu.Unlock()
	if quota > 0 && !m.blocked.Load() && m.used() >= quota {
		s.checkQuota(m)
	}
}

// meteredConn wraps a tunnel data stream: it counts every byte and applies
// the account's throughput cap. Reads carry local→visitor data (out), writes
// visitor→local data (in).
type meteredConn struct {
	net.Conn
	srv *Server
	m   *accountMeter
}

func (c *meteredConn) Read(b []byte) (int, error) {
	if c.m.blocked.Load() {
		return 0, errQuotaExceeded
	}
	if len(b) > meterBurst {
		b = b[:meterBurst]
	}
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.srv.addUsage(c.m, 0, int64(n))
		if _, out := c.m.limiters(); out != nil {
			out.WaitN(context.Background(), n)
		}
	}
	return n, err
}

func (c *meteredConn) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		if c.m.blocked.Load() {
			return written, errQuotaExceeded
		}
		chunk := b
		if len(chunk) > meterBurst {
			chunk = chunk[:meterBurst]
		}
		if in, _ := c.m.limiters(); in != nil {
			in.WaitN(context.Background(), len(chunk))
		}
		n, err := c.Conn.Write(chunk)
		written += n
		c.srv.addUsage(c.m, int64(n), 0)
		if err != nil {
			return written, err
		}
		b = b[n:]
	}
	return written, nil
}

// CloseWrite keeps half-closes working through the wrapper.
func (c *meteredConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Conn.Close()
}

// meterLoop flushes usage to the database and refreshes limits and usage.
func (s *Server) meterLoop(ctx context.Context) {
	flush := time.NewTicker(meterFlushEvery)
	refresh := time.NewTicker(meterRefresh)
	defer flush.Stop()
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			s.flushUsage(context.Background())
			return
		case <-flush.C:
			s.flushUsage(ctx)
		case <-refresh.C:
			s.flushUsage(ctx)
			s.refreshMeters(ctx, "")
		}
	}
}

func (s *Server) allMeters() []*accountMeter {
	s.meters.mu.Lock()
	defer s.meters.mu.Unlock()
	out := make([]*accountMeter, 0, len(s.meters.m))
	for _, m := range s.meters.m {
		out = append(out, m)
	}
	return out
}

func (s *Server) flushUsage(ctx context.Context) {
	day := time.Now().UTC().Format("2006-01-02")
	for _, m := range s.allMeters() {
		in, out := m.pendingIn.Swap(0), m.pendingOut.Swap(0)
		req, conns := m.pendingReq.Swap(0), m.pendingConn.Swap(0)
		if in == 0 && out == 0 && req == 0 && conns == 0 {
			continue
		}
		if err := s.store.AddUsage(ctx, m.userID, day, in, out, req, conns); err != nil {
			logf("usage for %s: %v", m.userID, err)
			m.pendingIn.Add(in)
			m.pendingOut.Add(out)
			m.pendingReq.Add(req)
			m.pendingConn.Add(conns)
			continue
		}
		m.mu.Lock()
		m.monthUsed += in + out
		m.mu.Unlock()
	}
}

// refreshMeters reloads limits/usage for one account (userID) or all; idle
// meters without tunnels are dropped.
func (s *Server) refreshMeters(ctx context.Context, userID string) {
	active := map[string]bool{}
	for _, t := range s.reg.Tunnels() {
		active[t.UserID] = true
	}
	for _, m := range s.allMeters() {
		if userID != "" && m.userID != userID {
			continue
		}
		if userID == "" && !active[m.userID] && time.Since(time.Unix(m.lastUsed.Load(), 0)) > 10*time.Minute &&
			m.pendingIn.Load()+m.pendingOut.Load()+m.pendingReq.Load()+m.pendingConn.Load() == 0 {
			s.meters.mu.Lock()
			delete(s.meters.m, m.userID)
			s.meters.mu.Unlock()
			continue
		}
		if err := s.refreshMeter(ctx, m); err != nil {
			logf("refresh limits for %s: %v", m.userID, err)
		}
	}
}
