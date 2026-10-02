package server

import (
	"context"
	"fmt"
	"time"

	"tund/internal/protocol"
)

// Tunnels can have a maximum lifetime: the setting limit_tunnel_lifetime
// (minutes, 0 = unlimited; admins are exempt) or the per-account override
// users.tunnel_lifetime_minutes. Each node closes its own tunnels once they
// are older than that; the client can start them again.

const (
	lifetimeSweep  = 15 * time.Second
	lifetimeNotice = 5 * time.Minute // tell the client this long before closing
	// expiryClientVersion is the first client that shows protocol.Message.ExpiresAt.
	expiryClientVersion = "0.5.0"
)

func effectiveLifetime(globalMinutes int, isAdmin bool, override *int) time.Duration {
	m := globalMinutes
	if isAdmin {
		m = 0
	}
	if override != nil {
		m = *override
	}
	if m <= 0 {
		return 0
	}
	return time.Duration(m) * time.Minute
}

func (s *Server) tunnelLifetime(ctx context.Context, userID string) (time.Duration, error) {
	plan, l, err := s.accountPlan(ctx, userID)
	if err != nil {
		return 0, err
	}
	return effectiveLifetime(plan.TunnelLifetime, l.IsAdmin, l.TunnelLifetime), nil
}

func (s *Server) lifetimeLoop(ctx context.Context) {
	t := time.NewTicker(lifetimeSweep)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.enforceLifetimes(ctx)
		}
	}
}

// enforceLifetimes closes the local tunnels that are past their account's
// lifetime. Changed settings and overrides apply on the next sweep.
func (s *Server) enforceLifetimes(ctx context.Context) {
	limits := map[string]time.Duration{}
	now := time.Now()
	for _, t := range s.reg.Tunnels() {
		max, seen := limits[t.UserID]
		if !seen {
			var err error
			if max, err = s.tunnelLifetime(ctx, t.UserID); err != nil {
				logf("tunnel lifetime for %s: %v", t.UserID, err)
				continue
			}
			limits[t.UserID] = max
		}
		if max <= 0 {
			continue
		}
		end := t.StartedAt.Add(max)
		switch left := end.Sub(now); {
		case left <= 0:
			t.session.unbindCode(t.BindID, fmt.Sprintf("this tunnel reached the maximum lifetime of %s on this server; start it again", formatLifetime(max)), protocol.CodeLifetime)
		case left <= lifetimeNotice && t.expiryNoted.CompareAndSwap(false, true):
			t.session.ctrl.Send(protocol.Message{
				Type: protocol.TypeNotice, ID: t.BindID, Code: protocol.CodeLifetime, ExpiresAt: &end,
				Error: fmt.Sprintf("%s closes in %s (maximum tunnel lifetime of %s)", t.PublicURL, formatLeft(left), formatLifetime(max)),
			})
		}
	}
}

// formatLifetime renders a lifetime the way people set it: 45m, 2h, 1h30m, 3d.
func formatLifetime(d time.Duration) string {
	m := int(d / time.Minute)
	switch {
	case m >= 1440 && m%1440 == 0:
		return fmt.Sprintf("%dd", m/1440)
	case m >= 60 && m%60 == 0:
		return fmt.Sprintf("%dh", m/60)
	case m > 60:
		return fmt.Sprintf("%dh%dm", m/60, m%60)
	}
	return fmt.Sprintf("%dm", m)
}

// formatLeft rounds a remaining time up to whole minutes: "5m", "1m".
func formatLeft(d time.Duration) string {
	return formatLifetime(max(time.Minute, (d + time.Minute - 1).Truncate(time.Minute)))
}
