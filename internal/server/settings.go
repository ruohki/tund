package server

import (
	"context"
	"encoding/json"
	"strings"
)

// Runtime holds the instance settings admins can change in the dashboard
// (settings table), falling back to the TUND_* environment. See docs/SPEC.md
// "Admin area, settings and email".
type Runtime struct {
	MaxTunnelsPerUser        int
	MaxPinnedPerUser         int // static addresses: hostnames + TCP ports
	MaxDomainsPerUser        int
	MaxTeamsPerUser          int
	AutoPin                  bool
	BrowserWarning           bool
	AbuseContact             string
	RetentionDays            int
	CaptureMaxBody           int
	RequireEmailVerification bool
	SMTPConfigured           bool
	BandwidthKbps            int    // per account and direction
	TransferGB               int    // per account and month
	CustomDomains            bool   // users.custom_domains overrides it
	Passthrough              bool   // TCP and TLS tunnels; users.passthrough overrides it
	InstanceName             string // Admin → Settings → Branding; shown on the edge's pages

	// Abuse protection
	UntrustedCustomDomains string // allow | review | deny
	WarnCustomDomains      bool
	UntrustedTCP           bool
	UntrustedTLS           bool
	BlockedWords           []string
	SafeBrowsingKey        string
	PhishingHeuristics     bool
	PhishingAutoBlock      bool
}

func (s *Server) rt() *Runtime { return s.runtime.Load() }

// defaultInstanceName matches the dashboard's default for instance_name.
const defaultInstanceName = "TUNd"

func defaultRuntime(c *Config) *Runtime {
	return &Runtime{
		MaxTunnelsPerUser: c.MaxTunnelsPerUser,
		MaxPinnedPerUser:  c.MaxPinnedPerUser,
		MaxDomainsPerUser: c.MaxDomainsPerUser,
		MaxTeamsPerUser:   c.MaxTeamsPerUser,
		AutoPin:           c.AutoPin,
		BrowserWarning:    c.BrowserWarning,
		AbuseContact:      c.AbuseContact,
		RetentionDays:     c.RetentionDays,
		CaptureMaxBody:    c.CaptureMaxBody,
		BandwidthKbps:     c.BandwidthKbps,
		TransferGB:        c.TransferGB,
		CustomDomains:     c.CustomDomains,
		Passthrough:       c.Passthrough,
		InstanceName:      defaultInstanceName,

		UntrustedCustomDomains: c.UntrustedCustomDomains,
		WarnCustomDomains:      true,
		UntrustedTCP:           c.UntrustedTCP,
		UntrustedTLS:           c.UntrustedTLS,
		BlockedWords:           defaultBlockedWords,
		SafeBrowsingKey:        c.SafeBrowsingKey,
		PhishingHeuristics:     true,
	}
}

// loadSettings rebuilds the runtime settings from the environment defaults
// and the settings table, then applies them to live tunnels.
func (s *Server) loadSettings(ctx context.Context) error {
	r := defaultRuntime(s.cfg)
	rows, err := s.store.Settings(ctx)
	if err != nil {
		return err
	}
	for key, raw := range rows {
		var ok bool
		switch key {
		case "limit_tunnels":
			ok = setInt(raw, &r.MaxTunnelsPerUser, 0, 1<<20)
		case "limit_pinned":
			ok = setInt(raw, &r.MaxPinnedPerUser, 0, 1<<20)
		case "limit_domains":
			ok = setInt(raw, &r.MaxDomainsPerUser, 0, 1<<20)
		case "limit_bandwidth_kbps":
			ok = setInt(raw, &r.BandwidthKbps, 0, 100_000_000)
		case "limit_transfer_gb":
			ok = setInt(raw, &r.TransferGB, 0, 1_000_000)
		case "instance_name":
			ok = json.Unmarshal(raw, &r.InstanceName) == nil
			if r.InstanceName = strings.TrimSpace(r.InstanceName); r.InstanceName == "" {
				r.InstanceName = defaultInstanceName
			}
		case "custom_domains":
			ok = json.Unmarshal(raw, &r.CustomDomains) == nil
		case "passthrough":
			ok = json.Unmarshal(raw, &r.Passthrough) == nil
		case "untrusted_custom_domains":
			var b bool
			var str string
			switch {
			case json.Unmarshal(raw, &b) == nil:
				r.UntrustedCustomDomains, ok = map[bool]string{true: "allow", false: "deny"}[b], true
			case json.Unmarshal(raw, &str) == nil && (str == "allow" || str == "review" || str == "deny"):
				r.UntrustedCustomDomains, ok = str, true
			}
		case "warn_custom_domains":
			ok = json.Unmarshal(raw, &r.WarnCustomDomains) == nil
		case "untrusted_tcp":
			ok = json.Unmarshal(raw, &r.UntrustedTCP) == nil
		case "untrusted_tls":
			ok = json.Unmarshal(raw, &r.UntrustedTLS) == nil
		case "blocked_hostname_words":
			ok = json.Unmarshal(raw, &r.BlockedWords) == nil
		case "safe_browsing_api_key":
			ok = json.Unmarshal(raw, &r.SafeBrowsingKey) == nil
		case "phishing_heuristics":
			ok = json.Unmarshal(raw, &r.PhishingHeuristics) == nil
		case "phishing_auto_block":
			ok = json.Unmarshal(raw, &r.PhishingAutoBlock) == nil
		case "limit_teams":
			ok = setInt(raw, &r.MaxTeamsPerUser, 0, 1<<20)
		case "auto_pin":
			ok = json.Unmarshal(raw, &r.AutoPin) == nil
		case "browser_warning":
			ok = json.Unmarshal(raw, &r.BrowserWarning) == nil
		case "abuse_contact":
			ok = json.Unmarshal(raw, &r.AbuseContact) == nil
		case "retention_days":
			ok = setInt(raw, &r.RetentionDays, 0, 3650)
		case "capture_max_body":
			ok = setInt(raw, &r.CaptureMaxBody, 1024, 10<<20)
		case "require_email_verification":
			ok = json.Unmarshal(raw, &r.RequireEmailVerification) == nil
		case "smtp":
			var smtp struct {
				Host string `json:"host"`
			}
			ok = json.Unmarshal(raw, &smtp) == nil
			r.SMTPConfigured = strings.TrimSpace(smtp.Host) != ""
		default:
			ok = true // dashboard-only settings
		}
		if !ok {
			logf("settings: ignoring invalid value for %s: %s", key, raw)
		}
	}
	s.runtime.Store(r)
	for _, t := range s.reg.Tunnels() {
		s.updateWarn(t)
	}
	s.refreshMeters(ctx, "")
	return nil
}

func setInt(raw json.RawMessage, dst *int, lo, hi int) bool {
	var n int
	if json.Unmarshal(raw, &n) != nil || n < lo || n > hi {
		return false
	}
	*dst = n
	return true
}

// emailVerificationRequired reports whether acct must verify its email
// address before using tunnels.
func (s *Server) emailVerificationRequired(acct *Account) bool {
	r := s.rt()
	return r.RequireEmailVerification && r.SMTPConfigured && !acct.EmailVerified && !acct.IsAdmin
}
