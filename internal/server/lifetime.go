package server

import (
	"context"
	"fmt"
	"time"
)

// Tunnels can have a maximum lifetime: the setting limit_tunnel_lifetime
// (minutes, 0 = unlimited; admins are exempt) or the per-account override
// users.tunnel_lifetime_minutes. Each node closes its own tunnels once they
// are older than that; the client can start them again.

const lifetimeSweep = 15 * time.Second

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
	l, err := s.store.UserLimits(ctx, userID)
	if err != nil {
		return 0, err
	}
	return effectiveLifetime(s.rt().TunnelLifetime, l.IsAdmin, l.TunnelLifetime), nil
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
		if max > 0 && now.Sub(t.StartedAt) >= max {
			t.session.unbind(t.BindID, fmt.Sprintf("this tunnel reached the maximum lifetime of %s on this server; start it again", formatLifetime(max)))
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
