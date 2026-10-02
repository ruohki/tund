package server

import "context"

// Plans (docs/SPEC.md "Plans and billing"): Free accounts get the instance
// limits, Pro accounts the pro_* settings plus custom domains and TCP/TLS
// tunnels. Whether an account is Pro is decided in Postgres (user_is_pro, from
// subscriptions, paid teams and admin grants). Per-account overrides
// (users.bandwidth_kbps, …) still win, and admins stay exempt where they were.

const (
	proDefaultTunnels = 10
	proDefaultPinned  = 10
	proDefaultDomains = 5
	proDefaultTeams   = 10
	proTransferFactor = 10 // default Pro transfer: 10× Free
)

// accountPlan holds an account's limits before its own overrides; 0 = unlimited.
type accountPlan struct {
	Pro                        bool
	Tunnels, Pinned, Domains   int
	Teams                      int
	BandwidthKbps, TransferGB  int
	TunnelLifetime             int // minutes
	CustomDomains, Passthrough bool
}

// noLower is a Pro limit that is never below the Free one (0 = unlimited).
func noLower(free, pro int) int {
	if free == 0 || pro == 0 {
		return 0
	}
	return max(free, pro)
}

func (r *Runtime) plan(pro bool) accountPlan {
	if pro {
		// Free's domain count only matters when Free has custom domains at all.
		domains := r.ProMaxDomains
		if r.CustomDomains {
			domains = noLower(r.MaxDomainsPerUser, r.ProMaxDomains)
		}
		return accountPlan{
			Pro:           true,
			Tunnels:       noLower(r.MaxTunnelsPerUser, r.ProMaxTunnels),
			Pinned:        noLower(r.MaxPinnedPerUser, r.ProMaxPinned),
			Domains:       domains,
			Teams:         noLower(r.MaxTeamsPerUser, r.ProMaxTeams),
			BandwidthKbps: noLower(r.BandwidthKbps, r.ProBandwidthKbps),
			TransferGB:    noLower(r.TransferGB, r.ProTransferGB),
			// A Free account without a lifetime limit can't get one with Pro either.
			TunnelLifetime: noLower(r.TunnelLifetime, r.ProTunnelLifetime),
			CustomDomains:  true, Passthrough: true,
		}
	}
	return accountPlan{
		Tunnels: r.MaxTunnelsPerUser, Pinned: r.MaxPinnedPerUser, Domains: r.MaxDomainsPerUser, Teams: r.MaxTeamsPerUser,
		BandwidthKbps: r.BandwidthKbps, TransferGB: r.TransferGB, TunnelLifetime: r.TunnelLifetime,
		CustomDomains: r.CustomDomains, Passthrough: r.Passthrough,
	}
}

// accountPlan returns the plan of an account.
func (s *Server) accountPlan(ctx context.Context, userID string) (accountPlan, *UserLimitRow, error) {
	l, err := s.store.UserLimits(ctx, userID)
	if err != nil {
		return accountPlan{}, nil, err
	}
	return s.rt().plan(l.Pro), l, nil
}
