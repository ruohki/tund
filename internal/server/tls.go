package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
	hetznerlegacy "github.com/libdns/hetzner"
	hetzner "github.com/libdns/hetzner/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Certs manages TLS certificates. Tunnel hostnames under the base domain are
// covered by a wildcard (DNS-01 or a manual cert file) when configured and
// otherwise obtained on demand, like custom domains, via HTTP-01/TLS-ALPN-01.
type Certs struct {
	srv         *Server
	pgStorage   *pgCertStorage    // set when TUND_CERT_STORAGE=postgres
	wildcardCfg *certmagic.Config // DNS-01 wildcard, started by Start
	cache       *certmagic.Cache
	onDemand    *certmagic.Config
	issuer      *certmagic.ACMEIssuer
	wildcard    bool // base subdomains are covered by a wildcard certificate
}

func newCerts(s *Server) *Certs {
	c := &Certs{srv: s}
	if s.cfg.TLSMode == "off" {
		return c
	}
	logger := certLogger()
	var storage certmagic.Storage = &certmagic.FileStorage{Path: s.cfg.CertDir}
	if s.cfg.CertStorage == "postgres" {
		c.pgStorage = newPGCertStorage(s.store.pool, s.cfg.NodeName()+"/"+randomToken(6))
		storage = c.pgStorage
	}

	var wildcardCfg *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(cert certmagic.Certificate) (*certmagic.Config, error) {
			if wildcardCfg != nil && slices.Contains(cert.Names, "*."+s.cfg.BaseDomain) {
				return wildcardCfg, nil
			}
			return c.onDemand, nil
		},
		Logger: logger,
	})

	c.cache = cache
	c.onDemand = certmagic.New(cache, certmagic.Config{
		Storage:  storage,
		Logger:   logger,
		OnDemand: &certmagic.OnDemandConfig{DecisionFunc: c.decide},
	})
	c.issuer = certmagic.NewACMEIssuer(c.onDemand, certmagic.ACMEIssuer{
		CA:     s.cfg.ACMECA,
		Email:  s.cfg.ACMEEmail,
		Agreed: true,
		Logger: logger,
	})
	c.onDemand.Issuers = []certmagic.Issuer{c.issuer}

	if s.cfg.DNSProvider != "" {
		var provider certmagic.DNSProvider
		var propagationDelay time.Duration
		switch s.cfg.DNSProvider {
		case "hetzner":
			provider = &hetzner.Provider{APIToken: s.cfg.DNSAPIToken}
			// Hetzner publishes new records to its nameservers after ~30-40s.
			propagationDelay = time.Minute
		case "hetzner-legacy":
			provider = &hetznerlegacy.Provider{AuthAPIToken: s.cfg.DNSAPIToken}
			propagationDelay = time.Minute
		case "cloudflare":
			provider = &cloudflare.Provider{APIToken: s.cfg.DNSAPIToken}
		}
		wildcardCfg = certmagic.New(cache, certmagic.Config{Storage: storage, Logger: logger})
		wildcardCfg.Issuers = []certmagic.Issuer{certmagic.NewACMEIssuer(wildcardCfg, certmagic.ACMEIssuer{
			CA:          s.cfg.ACMECA,
			Email:       s.cfg.ACMEEmail,
			Agreed:      true,
			Logger:      logger,
			DNS01Solver: &certmagic.DNS01Solver{DNSManager: certmagic.DNSManager{DNSProvider: provider, PropagationDelay: propagationDelay}},
		})}
		c.wildcard = true
		c.wildcardCfg = wildcardCfg
	}
	return c
}

// Start imports file certificates into shared storage (first start with
// TUND_CERT_STORAGE=postgres) and begins managing the wildcard certificate.
func (c *Certs) Start(ctx context.Context) error {
	if c.pgStorage != nil {
		n, err := c.pgStorage.importCertFiles(ctx, c.srv.cfg.CertDir)
		if err != nil {
			return fmt.Errorf("import certificates into postgres: %w", err)
		}
		if n > 0 {
			logf("imported %d certificate files from %s into postgres", n, c.srv.cfg.CertDir)
		}
	}
	if c.wildcardCfg != nil {
		// ManageAsync keeps retrying and renewing in the background. Only the
		// wildcard goes through DNS-01: an apex dashboard host would share the
		// _acme-challenge TXT name and the two orders would clobber each other,
		// so it is obtained via HTTP-01 like any other hostname (see Prewarm).
		if err := c.wildcardCfg.ManageAsync(context.Background(), []string{"*." + c.srv.cfg.BaseDomain}); err != nil {
			logf("wildcard certificate: %v", err)
		}
	}
	return nil
}

// certLogger logs certificate events (issuance, renewal, ACME errors) at info
// level in a compact console format.
func certLogger() *zap.Logger {
	zc := zap.NewProductionConfig()
	zc.Encoding = "console"
	zc.DisableStacktrace = true
	zc.DisableCaller = true
	zc.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006/01/02 15:04:05")
	zc.EncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	if os.Getenv("TUND_DEBUG") != "" {
		zc.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}
	l, err := zc.Build()
	if err != nil {
		return zap.NewNop()
	}
	return l.Named("tls")
}

// loadManual caches the operator-provided wildcard certificate.
func (c *Certs) loadManual(ctx context.Context) error {
	if c.srv.cfg.TLSMode != "manual" {
		return nil
	}
	if _, err := c.onDemand.CacheUnmanagedCertificatePEMFile(ctx, c.srv.cfg.TLSCertFile, c.srv.cfg.TLSKeyFile, nil); err != nil {
		return fmt.Errorf("load TLS certificate: %w", err)
	}
	c.wildcard = true
	return nil
}

func (c *Certs) underBase(name string) bool {
	base := c.srv.cfg.BaseDomain
	return strings.HasSuffix(name, "."+base) && !strings.Contains(strings.TrimSuffix(name, "."+base), ".")
}

// decide is the on-demand gate: only hostnames we actually serve get certificates,
// so scanners probing random names cannot burn through CA rate limits.
func (c *Certs) decide(ctx context.Context, name string) error {
	s := c.srv
	name = normalizeHost(name)
	if name == s.cfg.DashboardHost || s.cfg.RedirectHosts[name] {
		return nil
	}
	if _, blocked := s.blockedReason(name); blocked {
		return errors.New(name + " is blocked")
	}
	if s.reg.Lookup(name) != nil {
		return nil
	}
	if c.underBase(name) {
		if d, err := s.store.ResolveDomain(ctx, name); err == nil && d.Hostname == name {
			return nil
		}
		return errors.New("no tunnel for " + name)
	}
	d, err := s.store.ResolveDomain(ctx, name)
	if err == nil && d.Approval != "approved" {
		return errors.New(name + " is not approved")
	}
	if err == nil && d.Verified && d.Hostname == name {
		return nil
	}
	return errors.New("no verified domain or tunnel for " + name)
}

// Prewarm obtains a certificate for a freshly bound hostname so the first
// visitor does not wait for the CA.
func (c *Certs) Prewarm(host string) {
	if c.onDemand == nil || (c.wildcard && c.underBase(host)) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := c.onDemand.ObtainCertAsync(ctx, host); err != nil {
			logf("certificate for %s: %v", host, err)
		}
	}()
}

// StatusNames lists the certificate names worth reporting to admins.
func (c *Certs) StatusNames() []string {
	names := []string{c.srv.cfg.DashboardHost}
	if c.wildcard {
		names = append([]string{"*." + c.srv.cfg.BaseDomain}, names...)
	}
	return names
}

// Info describes the cached certificates for name (expiry, issuer).
func (c *Certs) Info(name string) []map[string]any {
	if c.cache == nil {
		return nil
	}
	var out []map[string]any
	for _, cert := range c.cache.AllMatchingCertificates(name) {
		if cert.Leaf == nil {
			continue
		}
		out = append(out, map[string]any{"name": name, "names": cert.Names, "not_after": cert.Leaf.NotAfter, "issuer": cert.Leaf.Issuer.CommonName})
		break
	}
	return out
}

func (c *Certs) TLSConfig() *tls.Config {
	tc := c.onDemand.TLSConfig()
	for _, p := range []string{"http/1.1", "h2"} {
		if !slices.Contains(tc.NextProtos, p) {
			tc.NextProtos = append([]string{p}, tc.NextProtos...)
		}
	}
	tc.MinVersion = tls.VersionTLS12
	return tc
}

func (c *Certs) HTTPChallengeHandler(h http.Handler) http.Handler {
	return c.issuer.HTTPChallengeHandler(h)
}
