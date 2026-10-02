package server

import "context"

// Custom domains need the feature on the account using them: the per-user
// override (users.custom_domains) when set, otherwise the instance setting,
// which admins don't need. TCP and TLS tunnels work the same way (passthrough.go).

// featureEnabled decides a per-account feature: the override wins, otherwise
// the instance setting, which admins don't need.
func featureEnabled(global, isAdmin bool, override *bool) bool {
	if override != nil {
		return *override
	}
	return global || isAdmin
}

func (s *Server) customDomainsAllowed(ctx context.Context, userID string) (bool, error) {
	plan, l, err := s.accountPlan(ctx, userID)
	if err != nil {
		return false, err
	}
	return featureEnabled(plan.CustomDomains, l.IsAdmin, l.CustomDomains), nil
}

// customDomainUsable: a custom domain of a team with a plan (Team, Team Pro,
// or granted) works for every member; anything else needs custom domains on
// the account.
func (s *Server) customDomainUsable(ctx context.Context, d *Domain, userID string) (bool, error) {
	if d != nil && d.TeamID != "" {
		plan, err := s.store.TeamPlan(ctx, d.TeamID)
		if err != nil {
			return false, err
		}
		if plan != "" {
			return true, nil
		}
	}
	return s.customDomainsAllowed(ctx, userID)
}

// recheckCustomDomains ends the custom-domain tunnels of accounts that may no
// longer use custom domains (userID "" = every account), after the setting, an
// override or a plan changed.
func (s *Server) recheckCustomDomains(ctx context.Context, userID string) {
	allowed := map[string]bool{}
	teamPlans := map[string]string{}
	for _, t := range s.reg.Tunnels() {
		if t.Hostname == "" || s.certs.underBase(t.Hostname) || (userID != "" && t.UserID != userID) {
			continue
		}
		if t.TeamID != "" {
			plan, seen := teamPlans[t.TeamID]
			if !seen {
				var err error
				if plan, err = s.store.TeamPlan(ctx, t.TeamID); err != nil {
					logf("team plan for %s: %v", t.Hostname, err)
					continue
				}
				teamPlans[t.TeamID] = plan
			}
			if plan != "" {
				continue
			}
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

const errCustomDomainsOff = "custom domains are not part of your plan on this server; upgrade in the dashboard or ask the administrator"
