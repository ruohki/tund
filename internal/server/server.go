// Package server implements tund-server: the TLS edge that accepts tund
// clients, routes public hostnames through their tunnels, records traffic and
// fronts the dashboard.
package server

import (
	"context"
	"crypto/rand"
	cryptotls "crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tund/internal/protocol"
)

// Version is set at build time.
var Version = "dev"

func logf(format string, args ...any) { log.Printf(format, args...) }

type Server struct {
	cfg      *Config
	store    *Store
	reg      *Registry
	recorder *Recorder
	certs    *Certs
	signer   signer
	limiter  loginLimiter
	// deviceLimiter throttles `tund login` code creation per IP.
	deviceLimiter loginLimiter
	reserved      map[string]bool

	dashboardProxy *httputil.ReverseProxy
	tcp            *tcpPorts // nil when TCP tunnels are disabled
	api            http.Handler

	jtiMu sync.Mutex
	jtis  map[string]time.Time

	userLocks sync.Map // user id -> *sync.Mutex, see lockUser

	runtime   atomic.Pointer[Runtime]           // settings editable in the dashboard
	meters    meters                            // per-account bandwidth meters
	blocked   atomic.Pointer[map[string]string] // blocked hostnames → reason
	phish     *phishScanner
	cluster   *cluster // nil in single-node mode
	startedAt time.Time

	oidcMu    sync.Mutex
	oidcCache map[string]oidcDiscovery
}

func New(cfg *Config, store *Store) (*Server, error) {
	s := &Server{
		cfg:       cfg,
		store:     store,
		reg:       NewRegistry(),
		recorder:  NewRecorder(store),
		signer:    signer{key: []byte(cfg.Secret)},
		jtis:      map[string]time.Time{},
		oidcCache: map[string]oidcDiscovery{},
		reserved:  map[string]bool{},
		startedAt: time.Now(),
		meters:    meters{m: map[string]*accountMeter{}},
		phish:     newPhishScanner(),
	}
	s.runtime.Store(defaultRuntime(cfg))
	for _, l := range strings.Fields("dashboard www api admin app connect edge tund mail smtp imap ftp ns ns1 ns2 status docs static assets cdn") {
		s.reserved[l] = true
	}
	if first, _, ok := strings.Cut(cfg.DashboardHost, "."); ok && strings.HasSuffix(cfg.DashboardHost, "."+cfg.BaseDomain) {
		s.reserved[first] = true
	}
	s.certs = newCerts(s)
	s.tcp = newTCPPorts(cfg.TCPPortFrom, cfg.TCPPortTo)
	if cfg.RelayURL != "" {
		c, err := newCluster(s)
		if err != nil {
			return nil, err
		}
		s.cluster = c
	}
	s.api = s.newAPI()
	s.dashboardProxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(cfg.DashboardUpstream)
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Proto", cfg.PublicScheme)
			pr.Out.Host = pr.In.Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			s.renderPage(w, r, http.StatusBadGateway, pageDashboardDown, map[string]any{"Detail": err.Error()})
		},
	}
	return s, nil
}

func (s *Server) reservedLabel(l string) bool { return s.reserved[l] }

// ServeHTTP routes by Host header.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := canonicalHost(r.Host)
	if host == s.cfg.DashboardHost {
		// Edge nodes serve the edge endpoints themselves and relay the
		// dashboard UI to a control node.
		if s.cluster != nil && s.cfg.Role == "edge" && !isRelayed(r) && !localDashboardPath(r.URL.Path) {
			if n, ok := s.cluster.controlNode(); ok {
				s.cluster.relayHTTP(w, r, n)
				return
			}
			s.renderPage(w, r, http.StatusBadGateway, pageDashboardDown, map[string]any{"Detail": "no control node is online"})
			return
		}
		s.serveDashboardHost(w, r)
		return
	}
	if s.cfg.RedirectHosts[host] {
		// Clients configured with the old host keep working: edge endpoints are
		// served in place (WebSocket handshakes do not follow redirects), pages
		// move to the dashboard's new address.
		if strings.HasPrefix(r.URL.Path, "/_tund/") {
			s.serveDashboardHost(w, r)
			return
		}
		http.Redirect(w, r, s.cfg.DashboardURL()+r.URL.RequestURI(), http.StatusPermanentRedirect)
		return
	}
	if _, blocked := s.blockedReason(host); blocked {
		s.renderPage(w, r, http.StatusUnavailableForLegalReasons, pageMessage, map[string]any{
			"Title":   "This site has been blocked",
			"Message": host + " was blocked by the operators of " + s.cfg.DashboardHost + " for violating the acceptable use policy.",
		})
		return
	}
	t := s.reg.Lookup(host)
	if t == nil {
		if s.cluster != nil && !isRelayed(r) {
			if n, proto, ok := s.cluster.ownerOf(r.Context(), host); ok && proto == protocol.ProtoHTTP {
				s.cluster.relayHTTP(w, r, n)
				return
			}
		}
		s.serveNoTunnel(w, r, host)
		return
	}
	if t.Proto != protocol.ProtoHTTP {
		// Only reachable without SNI (or over plain HTTP in TLS-off mode).
		s.renderPage(w, r, http.StatusMisdirectedRequest, pageMessage, map[string]any{
			"Title": "TLS passthrough tunnel", "Message": host + " forwards TLS connections to the owner's service; connect with a TLS client that sends SNI (e.g. https://" + host + ").",
		})
		return
	}
	if t.meter != nil && t.meter.blocked.Load() {
		t.meter.mu.Lock()
		quota, end := t.meter.quotaBytes, t.meter.periodEnd
		t.meter.mu.Unlock()
		s.renderPage(w, r, 509, pageMessage, map[string]any{
			"Title": "Bandwidth limit exceeded",
			"Message": fmt.Sprintf("%s is paused: its owner used up the monthly transfer quota of %s. Traffic resumes on %s (UTC).",
				host, humanBytes(quota), end.Format("2 Jan 2006")),
		})
		return
	}
	if !t.ipAllowed(r.RemoteAddr) {
		s.renderPage(w, r, http.StatusForbidden, pageMessage, map[string]any{
			"Title": "Access denied", "Message": "Your IP address (" + clientIP(r) + ") is not allowed to access " + host + ".",
		})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_tund/") && s.serveTunnelAuth(w, r, t) {
		return
	}
	if s.needsWarning(r, t) {
		s.renderWarning(w, r, t)
		return
	}
	id, ok := s.checkAccess(w, r, t)
	if !ok {
		return
	}
	s.serveTunnel(w, withIdentity(r, id), t, "")
}

func (s *Server) serveDashboardHost(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == protocol.ConnectPath:
		s.handleConnect(w, r)
	case p == "/_tund/install.sh" || p == "/_tund/install.ps1":
		s.serveInstall(w, r)
	case p == "/install.sh" || p == "/install.ps1":
		r.URL.Path = "/_tund" + p
		s.serveInstall(w, r)
	case strings.HasPrefix(p, apiPrefix+"/"):
		s.api.ServeHTTP(w, r)
	case p == "/_tund/device/code":
		s.handleDeviceCode(w, r)
	case p == "/_tund/device/token":
		s.handleDeviceToken(w, r)
	case strings.HasPrefix(p, "/_tund/downloads/"):
		s.serveDownload(w, r)
	case p == "/_tund/oidc/start":
		s.handleOIDCStart(w, r)
	case p == "/_tund/oidc/callback":
		s.handleOIDCCallback(w, r)
	case p == "/_tund/health":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": Version})
	default:
		s.dashboardProxy.ServeHTTP(w, r)
	}
}

func (s *Server) serveNoTunnel(w http.ResponseWriter, r *http.Request, host string) {
	data := map[string]any{"Host": host}
	if strings.HasSuffix(host, "."+s.cfg.BaseDomain) {
		data["Label"] = strings.TrimSuffix(host, "."+s.cfg.BaseDomain)
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if seen, ok := s.store.LastSeen(ctx, host); ok {
			data["LastSeen"] = humanAgo(time.Since(seen))
			s.renderPage(w, r, http.StatusNotFound, pageOffline, data)
			return
		}
	}
	s.renderPage(w, r, http.StatusNotFound, pageNotFound, data)
}

func humanAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments ago"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

func canonicalHost(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return normalizeHost(h)
}

func validLabel(l string) bool {
	if len(l) == 0 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for _, c := range l {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func validHostname(h string) bool {
	if len(h) > 253 || !strings.Contains(h, ".") {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if !validLabel(l) {
			return false
		}
	}
	return true
}

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// onConfigChange re-evaluates live tunnels after the dashboard changed
// domains, providers or tokens.
func (s *Server) onConfigChange(payload string) {
	var ev struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if payload != "" {
		json.Unmarshal([]byte(payload), &ev)
	}
	switch ev.Kind {
	case "authtoken_revoked":
		for _, as := range s.reg.Sessions() {
			if as.Account.TokenID == ev.ID {
				as.close("the authtoken used by this client was revoked")
			}
		}
		return
	case "user_disabled":
		for _, as := range s.reg.Sessions() {
			if as.Account.UserID == ev.ID {
				as.close("this account was disabled by an administrator")
			}
		}
		return
	case "user_updated":
		s.refreshWarning(ev.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.refreshMeters(ctx, ev.ID)
		return
	case "blocked_hosts":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.loadBlocked(ctx); err != nil {
			logf("reload blocked hosts: %v", err)
		}
		return
	case "settings":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.loadSettings(ctx); err != nil {
			logf("reload settings: %v", err)
		} else {
			logf("settings reloaded")
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, t := range s.reg.Tunnels() {
		if t.Proto == protocol.ProtoTCP {
			s.recheckTCP(ctx, t)
			continue
		}
		d, err := s.store.ResolveDomain(ctx, t.Hostname)
		if err != nil && !errors.Is(err, errNotFound) {
			logf("refresh policy for %s: %v", t.Hostname, err)
			continue
		}
		if d != nil && !s.canUseDomain(ctx, d, t.UserID) {
			d = nil
		}
		if !s.certs.underBase(t.Hostname) && (d == nil || !d.Verified || d.Approval != "approved") {
			reason := "the domain " + t.Hostname + " was removed from your account"
			if t.TeamID != "" {
				reason = "you can no longer use " + t.Hostname + " (it left your team, or you left the team)"
			}
			t.session.unbind(t.BindID, reason)
			continue
		}
		if t.TeamID != "" && (d == nil || d.TeamID != t.TeamID) {
			// A team static hostname the owner may no longer use.
			t.session.unbind(t.BindID, "you can no longer use "+t.Hostname+" (it left your team, or you left the team)")
			continue
		}
		if t.clientPolicy {
			continue
		}
		pol := domainPolicy(d)
		if pol.Fingerprint() != t.Policy().Fingerprint() {
			t.setPolicy(pol)
			s.store.SetTunnelAuthMode(ctx, t.ID, pol.Mode)
			logf("tunnel %s access policy is now %s", t.Hostname, pol.Mode)
		}
	}
}

// Run starts all listeners and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	if err := s.store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := s.store.CloseStale(ctx, s.cfg.NodeName()); err != nil {
		return err
	}
	if s.cfg.TLSMode != "off" {
		if err := s.certs.Start(ctx); err != nil {
			return err
		}
	}
	if err := s.certs.loadManual(ctx); err != nil {
		return err
	}
	if err := s.loadSettings(ctx); err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	if err := s.loadBlocked(ctx); err != nil {
		return fmt.Errorf("load blocked hosts: %w", err)
	}

	bg, stopBg := context.WithCancel(context.Background())
	defer stopBg()
	if s.cluster != nil {
		if err := s.cluster.run(bg); err != nil {
			return err
		}
	}
	recDone := make(chan struct{})
	go func() { s.recorder.Run(bg); close(recDone) }()
	go s.store.Listen(bg, "tund_config", s.onConfigChange)
	go s.meterLoop(bg)
	go s.safeBrowsingLoop(bg)
	go s.phishLoop(bg)
	go func() {
		for {
			if err := s.store.Prune(bg, s.rt().RetentionDays); err != nil && bg.Err() == nil {
				logf("prune: %v", err)
			}
			select {
			case <-bg.Done():
				return
			case <-time.After(time.Hour):
			}
		}
	}()

	errLog := log.New(&filteredLog{}, "", 0)
	var servers []*http.Server
	errc := make(chan error, 3)
	start := func(srv *http.Server, tls bool) {
		servers = append(servers, srv)
		go func() {
			var err error
			if tls {
				// TLS passthrough tunnels are split off before the handshake.
				var ln net.Listener
				if ln, err = net.Listen("tcp", srv.Addr); err == nil {
					err = srv.Serve(cryptotls.NewListener(newSNIRouter(ln, s), srv.TLSConfig))
				}
			} else {
				err = srv.ListenAndServe()
			}
			if !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s: %w", srv.Addr, err)
			}
		}()
	}
	newServer := func(addr string, h http.Handler) *http.Server {
		return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second, ErrorLog: errLog}
	}

	if s.cfg.TLSMode == "off" {
		start(newServer(s.cfg.HTTPAddr, s), false)
		logf("serving plain HTTP on %s (TLS off)", s.cfg.HTTPAddr)
	} else {
		httpsSrv := newServer(s.cfg.HTTPSAddr, s)
		httpsSrv.TLSConfig = s.certs.TLSConfig()
		start(httpsSrv, true)
		redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := canonicalHost(r.Host)
			if host == "" {
				http.Error(w, "missing host", http.StatusBadRequest)
				return
			}
			http.Redirect(w, r, s.cfg.PublicURL(host)+r.URL.RequestURI(), http.StatusPermanentRedirect)
		})
		start(newServer(s.cfg.HTTPAddr, s.certs.HTTPChallengeHandler(redirect)), false)
		logf("serving HTTPS on %s, HTTP redirects + ACME on %s", s.cfg.HTTPSAddr, s.cfg.HTTPAddr)
		// Let the listeners bind first: the ACME solvers must not grab :80/:443 themselves.
		time.AfterFunc(2*time.Second, func() { s.certs.Prewarm(s.cfg.DashboardHost) })
	}
	start(newServer(s.cfg.InternalAddr, s.internalAPI()), false)
	logf("tund-server %s: base domain %s, dashboard %s, internal API on %s", Version, s.cfg.BaseDomain, s.cfg.DashboardURL(), s.cfg.InternalAddr)

	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	logf("shutting down")
	for _, as := range s.reg.Sessions() {
		as.close("server shutting down")
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, srv := range servers {
		srv.Shutdown(shutCtx)
	}
	stopBg()
	<-recDone
	return err
}

// filteredLog drops the noisy TLS handshake errors produced by scanners.
type filteredLog struct{}

func (filteredLog) Write(p []byte) (int, error) {
	msg := string(p)
	if strings.Contains(msg, "TLS handshake error") || strings.Contains(msg, "http2: server") {
		return len(p), nil
	}
	log.Print(strings.TrimRight(msg, "\n"))
	return len(p), nil
}
