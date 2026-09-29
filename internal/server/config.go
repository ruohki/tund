package server

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	BaseDomain        string
	DashboardHost     string
	DatabaseURL       string
	Secret            string
	InternalSecret    string
	DashboardUpstream *url.URL

	HTTPAddr     string
	HTTPSAddr    string
	InternalAddr string

	TLSMode      string // acme | manual | off
	PublicScheme string
	PublicPort   string

	ACMEEmail   string
	ACMECA      string
	CertDir     string
	CertStorage string // file | postgres (shared by all nodes)

	// Cluster (edge nodes): empty RelayURL = single-node mode.
	Role        string // control | edge
	NodeRegion  string
	RelayAddr   string
	RelayURL    string
	DNSProvider string
	DNSAPIToken string
	TLSCertFile string
	TLSKeyFile  string

	CaptureMaxBody             int
	RetentionDays              int
	DownloadsDir               string
	DownloadBaseURL            string // where client binaries are published (GitHub Releases)
	MaxTunnelsPerUser          int
	MaxPinnedPerUser           int
	MaxDomainsPerUser          int    // enforced by the dashboard; reported by the API
	MaxTeamsPerUser            int    // enforced by the dashboard
	BandwidthKbps              int    // per account and direction; 0 = unlimited
	UntrustedCustomDomains     string // allow | review | deny
	UntrustedTCP, UntrustedTLS bool
	SafeBrowsingKey            string
	TransferGB                 int // per account and month; 0 = unlimited
	AutoPin                    bool
	BrowserWarning             bool   // show the anti-phishing interstitial to browser visitors
	AbuseContact               string // email or URL shown on the warning page
	// TCP tunnels: public port range (0/0 = disabled) and the host shown in URLs.
	TCPPortFrom, TCPPortTo int
	TCPHost                string

	// RedirectHosts permanently redirect to the dashboard (e.g. a previous
	// dashboard domain after a move).
	RedirectHosts map[string]bool
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) (int, error) {
	v := env(key, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func envBool(key string, def bool) (bool, error) {
	switch strings.ToLower(env(key, "")) {
	case "":
		return def, nil
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("%s must be true or false", key)
}

// LoadConfig reads the TUND_* environment variables documented in docs/SPEC.md.
func LoadConfig() (*Config, error) {
	c := &Config{
		BaseDomain:     normalizeHost(env("TUND_BASE_DOMAIN", "")),
		DatabaseURL:    env("TUND_DATABASE_URL", ""),
		Secret:         env("TUND_SECRET", ""),
		InternalSecret: env("TUND_INTERNAL_SECRET", ""),
		HTTPAddr:       env("TUND_HTTP_ADDR", ":80"),
		HTTPSAddr:      env("TUND_HTTPS_ADDR", ":443"),
		InternalAddr:   env("TUND_INTERNAL_ADDR", ":4040"),
		TLSMode:        env("TUND_TLS_MODE", "acme"),
		PublicPort:     env("TUND_PUBLIC_PORT", ""),
		ACMEEmail:      env("TUND_ACME_EMAIL", ""),
		ACMECA:         env("TUND_ACME_CA", ""),
		CertDir:        env("TUND_CERT_DIR", "/data/certs"),
		DNSProvider:    env("TUND_DNS_PROVIDER", ""),
		DNSAPIToken:    env("TUND_DNS_API_TOKEN", ""),
		TLSCertFile:    env("TUND_TLS_CERT_FILE", ""),
		TLSKeyFile:     env("TUND_TLS_KEY_FILE", ""),
		DownloadsDir:   env("TUND_DOWNLOADS_DIR", ""),
	}
	var errs []error
	if c.BaseDomain == "" {
		errs = append(errs, errors.New("TUND_BASE_DOMAIN is required"))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("TUND_DATABASE_URL is required"))
	}
	if len(c.Secret) < 16 {
		errs = append(errs, errors.New("TUND_SECRET is required (at least 16 characters)"))
	}
	if len(c.InternalSecret) < 16 {
		errs = append(errs, errors.New("TUND_INTERNAL_SECRET is required (at least 16 characters)"))
	}
	c.DashboardHost = normalizeHost(env("TUND_DASHBOARD_HOST", "dashboard."+c.BaseDomain))

	switch c.TLSMode {
	case "acme", "manual":
	case "off":
	default:
		errs = append(errs, fmt.Errorf("TUND_TLS_MODE must be acme, manual or off (got %q)", c.TLSMode))
	}
	if c.TLSMode == "manual" && (c.TLSCertFile == "" || c.TLSKeyFile == "") {
		errs = append(errs, errors.New("TUND_TLS_MODE=manual needs TUND_TLS_CERT_FILE and TUND_TLS_KEY_FILE"))
	}
	switch c.DNSProvider {
	case "", "hetzner", "hetzner-legacy", "cloudflare":
	default:
		errs = append(errs, fmt.Errorf("TUND_DNS_PROVIDER %q is not supported (hetzner, hetzner-legacy, cloudflare)", c.DNSProvider))
	}
	if c.DNSProvider != "" && c.DNSAPIToken == "" {
		errs = append(errs, errors.New("TUND_DNS_PROVIDER needs TUND_DNS_API_TOKEN"))
	}

	defScheme := "https"
	if c.TLSMode == "off" {
		defScheme = "http"
	}
	c.PublicScheme = env("TUND_PUBLIC_SCHEME", defScheme)

	up, err := url.Parse(env("TUND_DASHBOARD_UPSTREAM", "http://dashboard:3000"))
	if err != nil {
		errs = append(errs, fmt.Errorf("TUND_DASHBOARD_UPSTREAM: %w", err))
	}
	c.DashboardUpstream = up

	if c.CaptureMaxBody, err = envInt("TUND_CAPTURE_MAX_BODY", 256*1024); err != nil {
		errs = append(errs, err)
	}
	if c.RetentionDays, err = envInt("TUND_RETENTION_DAYS", 7); err != nil {
		errs = append(errs, err)
	}
	if c.MaxTunnelsPerUser, err = envInt("TUND_MAX_TUNNELS_PER_USER", 0); err != nil {
		errs = append(errs, err)
	}
	if c.MaxPinnedPerUser, err = envInt("TUND_MAX_PINNED_PER_USER", 0); err != nil {
		errs = append(errs, err)
	}
	if c.MaxDomainsPerUser, err = envInt("TUND_MAX_DOMAINS_PER_USER", 0); err != nil {
		errs = append(errs, err)
	}
	if c.MaxTeamsPerUser, err = envInt("TUND_MAX_TEAMS_PER_USER", 3); err != nil {
		errs = append(errs, err)
	}
	switch v := strings.ToLower(env("TUND_UNTRUSTED_CUSTOM_DOMAINS", "review")); v {
	case "allow", "true", "1", "yes", "on":
		c.UntrustedCustomDomains = "allow"
	case "deny", "false", "0", "no", "off":
		c.UntrustedCustomDomains = "deny"
	case "review":
		c.UntrustedCustomDomains = "review"
	default:
		errs = append(errs, fmt.Errorf("TUND_UNTRUSTED_CUSTOM_DOMAINS must be allow, review or deny (got %q)", v))
	}
	for key, dst := range map[string]*bool{
		"TUND_UNTRUSTED_TCP": &c.UntrustedTCP,
		"TUND_UNTRUSTED_TLS": &c.UntrustedTLS,
	} {
		if *dst, err = envBool(key, true); err != nil {
			errs = append(errs, err)
		}
	}
	c.SafeBrowsingKey = env("TUND_SAFE_BROWSING_API_KEY", "")
	if c.BandwidthKbps, err = envInt("TUND_BANDWIDTH_KBPS", 0); err != nil {
		errs = append(errs, err)
	}
	if c.TransferGB, err = envInt("TUND_TRANSFER_GB", 0); err != nil {
		errs = append(errs, err)
	}
	if pr := env("TUND_TCP_PORTS", ""); pr != "" {
		from, to, ok := strings.Cut(pr, "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(from))
		b, err2 := strconv.Atoi(strings.TrimSpace(to))
		if !ok || err1 != nil || err2 != nil || a < 1024 || b > 65535 || a > b {
			errs = append(errs, fmt.Errorf("TUND_TCP_PORTS must look like 20000-20999 (ports 1024-65535), got %q", pr))
		} else {
			c.TCPPortFrom, c.TCPPortTo = a, b
		}
	}
	c.TCPHost = normalizeHost(env("TUND_TCP_HOST", ""))
	c.Role = env("TUND_ROLE", "control")
	if c.Role != "control" && c.Role != "edge" {
		errs = append(errs, fmt.Errorf("TUND_ROLE must be control or edge (got %q)", c.Role))
	}
	c.NodeRegion = env("TUND_NODE_REGION", "")
	c.RelayAddr = env("TUND_RELAY_ADDR", ":4443")
	c.RelayURL = env("TUND_RELAY_URL", "")
	if c.Role == "edge" && c.RelayURL == "" {
		errs = append(errs, errors.New("TUND_ROLE=edge needs TUND_RELAY_URL (how other nodes reach this node)"))
	}
	if c.RelayURL != "" && env("TUND_CERT_STORAGE", "") != "postgres" {
		errs = append(errs, errors.New("edge nodes (TUND_RELAY_URL set) need TUND_CERT_STORAGE=postgres so all nodes share certificates and the ACME account"))
	}
	switch c.CertStorage = env("TUND_CERT_STORAGE", "file"); c.CertStorage {
	case "file", "postgres":
	default:
		errs = append(errs, fmt.Errorf("TUND_CERT_STORAGE must be file or postgres (got %q)", c.CertStorage))
	}
	c.DownloadBaseURL = strings.TrimRight(env("TUND_DOWNLOAD_BASE_URL", "https://github.com/ruohki/tund/releases/latest/download"), "/")
	c.RedirectHosts = map[string]bool{}
	for _, h := range strings.FieldsFunc(env("TUND_REDIRECT_HOSTS", ""), func(r rune) bool { return r == ',' || r == ' ' }) {
		if h = normalizeHost(h); h != "" && h != c.DashboardHost {
			c.RedirectHosts[h] = true
		}
	}
	if c.AutoPin, err = envBool("TUND_AUTO_PIN", true); err != nil {
		errs = append(errs, err)
	}
	if c.BrowserWarning, err = envBool("TUND_BROWSER_WARNING", false); err != nil {
		errs = append(errs, err)
	}
	c.AbuseContact = env("TUND_ABUSE_CONTACT", "")
	return c, errors.Join(errs...)
}

// NodeName identifies this server process among the nodes of a cluster.
func (c *Config) NodeName() string {
	if n := env("TUND_NODE_NAME", ""); n != "" {
		return n
	}
	h, _ := os.Hostname()
	return h
}

// PublicURL returns the externally visible URL for a hostname.
func (c *Config) PublicURL(host string) string {
	u := c.PublicScheme + "://" + host
	if c.PublicPort != "" && !(c.PublicScheme == "https" && c.PublicPort == "443") && !(c.PublicScheme == "http" && c.PublicPort == "80") {
		u += ":" + c.PublicPort
	}
	return u
}

func (c *Config) DashboardURL() string { return c.PublicURL(c.DashboardHost) }

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}
