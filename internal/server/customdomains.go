package server

import "context"

// Custom domains need the feature on the account using them: the per-user
// override (users.custom_domains) when set, otherwise the instance setting,
// which admins don't need.

func customDomainsEnabled(global, isAdmin bool, override *bool) bool {
	if override != nil {
		return *override
	}
	return global || isAdmin
}

func (s *Server) customDomainsAllowed(ctx context.Context, userID string) (bool, error) {
	l, err := s.store.UserLimits(ctx, userID)
	if err != nil {
		return false, err
	}
	return customDomainsEnabled(s.rt().CustomDomains, l.IsAdmin, l.CustomDomains), nil
}

// recheckCustomDomains ends the custom-domain tunnels of accounts that may no
// longer use custom domains (userID "" = every account), after the setting or
// an override changed.
func (s *Server) recheckCustomDomains(ctx context.Context, userID string) {
	allowed := map[string]bool{}
	for _, t := range s.reg.Tunnels() {
		if t.Hostname == "" || s.certs.underBase(t.Hostname) || (userID != "" && t.UserID != userID) {
			continue
		}
		ok, seen := allowed[t.UserID]
		if !seen {
			var err error
			if ok, err = s.customDomainsAllowed(ctx, t.UserID); err != nil {
				logf("custom domains check for %s: %v", t.Hostname, err)
				continue
			}
			allowed[t.UserID] = ok
		}
		if !ok {
			t.session.unbind(t.BindID, errCustomDomainsOff)
		}
	}
}

const errCustomDomainsOff = "custom domains are not enabled for your account on this server; ask the administrator"
