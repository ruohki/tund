package server

import (
	"context"

	"tund/internal/protocol"
)

// TCP and TLS passthrough tunnels need the feature on the account, like custom
// domains: the per-user override (users.passthrough) when set, otherwise the
// instance setting (passthrough, default off), which admins don't need. The
// untrusted-account settings (untrusted_tcp, untrusted_tls) still apply on top.

func (s *Server) passthroughAllowed(ctx context.Context, userID string) (bool, error) {
	l, err := s.store.UserLimits(ctx, userID)
	if err != nil {
		return false, err
	}
	return featureEnabled(s.rt().Passthrough, l.IsAdmin, l.Passthrough), nil
}

// recheckPassthrough ends the TCP and TLS tunnels of accounts that may no
// longer open them (userID "" = every account), after the setting or an
// override changed.
func (s *Server) recheckPassthrough(ctx context.Context, userID string) {
	allowed := map[string]bool{}
	for _, t := range s.reg.Tunnels() {
		if (t.Proto != protocol.ProtoTCP && t.Proto != protocol.ProtoTLS) || (userID != "" && t.UserID != userID) {
			continue
		}
		ok, seen := allowed[t.UserID]
		if !seen {
			var err error
			if ok, err = s.passthroughAllowed(ctx, t.UserID); err != nil {
				logf("passthrough check for %s tunnel %s: %v", t.Proto, t.BindID, err)
				continue
			}
			allowed[t.UserID] = ok
		}
		if !ok {
			t.session.unbind(t.BindID, errPassthroughOff)
		}
	}
}

const errPassthroughOff = "TCP and TLS tunnels are not enabled for your account on this server; ask the administrator"
