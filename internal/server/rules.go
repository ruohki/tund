package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"tund/internal/protocol"
)

// Traffic rules of HTTP tunnels (protocol.Rules), applied by the edge that
// holds the tunnel: header rewrites, CORS, a per-visitor rate limit and path
// routes to other local addresses.

const (
	maxRoutes       = 16
	maxHeaderRules  = 32
	maxCORSOrigins  = 32
	maxRateVisitors = 50_000 // visitors tracked per tunnel; beyond that new ones pass
	defaultCORSAge  = 600
	corsMethods     = "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS"
	limitNoticeGap  = time.Minute // at most one "rate limited" notice per tunnel and minute
)

type tunnelRules struct {
	reqSet, respSet       http.Header
	reqRemove, respRemove []string
	cors                  *corsRule
	limit                 *visitorLimiter
	routes                []*tunnelRoute // longest prefix first
	// spec is the canonical form; load-balanced tunnels must agree on it.
	spec string
}

type corsRule struct {
	anyOrigin   bool
	origins     map[string]bool
	methods     string
	headers     string // empty = echo Access-Control-Request-Headers
	expose      string
	credentials bool
	maxAge      string
}

type tunnelRoute struct {
	index      int // protocol.StreamHeader.Route
	prefix     string
	strip      bool
	local      *url.URL
	localAddr  string
	hostHeader string // "" = the public host
}

// hopHeaders and the edge's own headers cannot be changed by rules.
var protectedHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true, "Upgrade": true,
	"Keep-Alive": true, "Te": true, "Trailer": true, "Proxy-Connection": true,
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", c):
		default:
			return false
		}
	}
	return true
}

func checkRuleHeader(kind, name string) (string, error) {
	name = strings.TrimSpace(name)
	if !validHeaderName(name) {
		return "", bindError(fmt.Sprintf("invalid %s header name %q", kind, name))
	}
	canon := http.CanonicalHeaderKey(name)
	if protectedHeaders[canon] {
		return "", bindError(fmt.Sprintf("the %s header %s cannot be changed by a rule", kind, canon))
	}
	if strings.HasPrefix(canon, "X-Tund-") {
		return "", bindError("X-Tund-* headers are set by the server itself and cannot be changed by a rule")
	}
	return canon, nil
}

func compileHeaderRules(kind string, hr *protocol.HeaderRules) (http.Header, []string, error) {
	if hr == nil {
		return nil, nil, nil
	}
	if len(hr.Set)+len(hr.Remove) > maxHeaderRules {
		return nil, nil, bindError(fmt.Sprintf("too many %s header rules (at most %d)", kind, maxHeaderRules))
	}
	set := http.Header{}
	for k, v := range hr.Set {
		name, err := checkRuleHeader(kind, k)
		if err != nil {
			return nil, nil, err
		}
		if strings.ContainsAny(v, "\r\n\x00") || len(v) > 4096 {
			return nil, nil, bindError(fmt.Sprintf("invalid value for the %s header %s", kind, name))
		}
		set.Set(name, strings.TrimSpace(v))
	}
	var remove []string
	for _, k := range hr.Remove {
		name, err := checkRuleHeader(kind, k)
		if err != nil {
			return nil, nil, err
		}
		if !slices.Contains(remove, name) {
			remove = append(remove, name)
		}
	}
	sort.Strings(remove)
	return set, remove, nil
}

// parseRateLimit reads "100/m", "10/s" or "5000/h".
func parseRateLimit(s string) (int, time.Duration, error) {
	n, unit, ok := strings.Cut(strings.ReplaceAll(strings.ToLower(s), " ", ""), "/")
	count, err := strconv.Atoi(n)
	if !ok || err != nil || count < 1 || count > 1_000_000 {
		return 0, 0, bindError(fmt.Sprintf("invalid rate limit %q: use requests per s, m or h, e.g. 100/m", s))
	}
	switch unit {
	case "s", "sec", "second":
		return count, time.Second, nil
	case "m", "min", "minute":
		return count, time.Minute, nil
	case "h", "hour":
		return count, time.Hour, nil
	}
	return 0, 0, bindError(fmt.Sprintf("invalid rate limit %q: use requests per s, m or h, e.g. 100/m", s))
}

func normalizeOrigin(o string) (string, bool) {
	o = strings.TrimRight(strings.ToLower(strings.TrimSpace(o)), "/")
	if o == "*" {
		return o, true
	}
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

func compileCORS(c *protocol.CORS) (*corsRule, error) {
	if c == nil {
		return nil, nil
	}
	if len(c.Origins) == 0 {
		return nil, bindError("CORS needs at least one origin (or \"*\")")
	}
	if len(c.Origins) > maxCORSOrigins {
		return nil, bindError(fmt.Sprintf("too many CORS origins (at most %d)", maxCORSOrigins))
	}
	r := &corsRule{origins: map[string]bool{}, methods: corsMethods, credentials: c.Credentials, maxAge: strconv.Itoa(defaultCORSAge)}
	for _, o := range c.Origins {
		n, ok := normalizeOrigin(o)
		if !ok {
			return nil, bindError(fmt.Sprintf("invalid CORS origin %q: use scheme://host[:port] or *", o))
		}
		if n == "*" {
			r.anyOrigin = true
		} else {
			r.origins[n] = true
		}
	}
	if r.anyOrigin && r.credentials {
		return nil, bindError("CORS with credentials needs explicit origins, not *")
	}
	list := func(kind string, vals []string, upper bool) (string, error) {
		var out []string
		for _, v := range vals {
			v = strings.TrimSpace(v)
			if !validHeaderName(v) {
				return "", bindError(fmt.Sprintf("invalid CORS %s %q", kind, v))
			}
			if upper {
				v = strings.ToUpper(v)
			}
			out = append(out, v)
		}
		return strings.Join(out, ", "), nil
	}
	var err error
	if len(c.Methods) > 0 {
		if r.methods, err = list("method", c.Methods, true); err != nil {
			return nil, err
		}
	}
	if r.headers, err = list("header", c.Headers, false); err != nil {
		return nil, err
	}
	if r.expose, err = list("header", c.Expose, false); err != nil {
		return nil, err
	}
	if c.MaxAge < 0 || c.MaxAge > 86400 {
		return nil, bindError("CORS max_age must be between 0 and 86400 seconds")
	}
	if c.MaxAge > 0 {
		r.maxAge = strconv.Itoa(c.MaxAge)
	}
	return r, nil
}

func normalizeRoutePrefix(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "?#* \t") {
		return "", false
	}
	p = strings.TrimRight(p, "/")
	return p, p != ""
}

// compileRules validates the rules of a bind. hostHeader is the bind's
// host_header option, which routes apply to their own local address.
func compileRules(in *protocol.Rules, hostHeader string) (*tunnelRules, error) {
	if in == nil {
		return nil, nil
	}
	r := &tunnelRules{}
	var err error
	if r.reqSet, r.reqRemove, err = compileHeaderRules("request", in.RequestHeaders); err != nil {
		return nil, err
	}
	if r.respSet, r.respRemove, err = compileHeaderRules("response", in.ResponseHeaders); err != nil {
		return nil, err
	}
	if r.cors, err = compileCORS(in.CORS); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.RateLimit) != "" {
		n, per, err := parseRateLimit(in.RateLimit)
		if err != nil {
			return nil, err
		}
		r.limit = newVisitorLimiter(n, per)
	}
	if len(in.Routes) > maxRoutes {
		return nil, bindError(fmt.Sprintf("too many routes (at most %d)", maxRoutes))
	}
	seen := map[string]bool{}
	for i, rt := range in.Routes {
		prefix, ok := normalizeRoutePrefix(rt.Path)
		if !ok {
			return nil, bindError(fmt.Sprintf("invalid route path %q: use a path prefix like /api (the tunnel's own address serves /)", rt.Path))
		}
		if seen[prefix] {
			return nil, bindError("two routes for " + prefix)
		}
		seen[prefix] = true
		local, err := url.Parse(rt.LocalAddr)
		if err != nil || (local.Scheme != "http" && local.Scheme != "https") || local.Host == "" {
			return nil, bindError(fmt.Sprintf("invalid local address %q for route %s", rt.LocalAddr, prefix))
		}
		route := &tunnelRoute{index: i + 1, prefix: prefix, strip: rt.StripPrefix, local: local, localAddr: rt.LocalAddr}
		switch hostHeader {
		case "", "preserve":
		case "rewrite":
			route.hostHeader = local.Host
		default:
			route.hostHeader = hostHeader
		}
		r.routes = append(r.routes, route)
	}
	sort.SliceStable(r.routes, func(i, j int) bool { return len(r.routes[i].prefix) > len(r.routes[j].prefix) })
	if r.reqSet == nil && r.respSet == nil && r.reqRemove == nil && r.respRemove == nil && r.cors == nil && r.limit == nil && len(r.routes) == 0 {
		return nil, nil
	}
	spec, _ := json.Marshal(in)
	r.spec = string(spec)
	return r, nil
}

// route returns the route for a request path, nil for the tunnel's own address.
func (r *tunnelRules) route(path string) *tunnelRoute {
	if r == nil {
		return nil
	}
	for _, rt := range r.routes {
		if path == rt.prefix || strings.HasPrefix(path, rt.prefix+"/") {
			return rt
		}
	}
	return nil
}

func (rt *tunnelRoute) stripPath(u *url.URL) {
	if !rt.strip {
		return
	}
	trim := func(p string) string {
		p = strings.TrimPrefix(p, rt.prefix)
		if p == "" || p[0] != '/' {
			p = "/" + p
		}
		return p
	}
	u.Path = trim(u.Path)
	if u.RawPath != "" {
		if strings.HasPrefix(u.RawPath, rt.prefix) {
			u.RawPath = trim(u.RawPath)
		} else {
			u.RawPath = ""
		}
	}
}

// routeHost is the URL host the proxy uses for a route: it keeps each
// route's idle connections apart and tells DialContext which one to open.
func routeHost(index int) string { return "route-" + strconv.Itoa(index) + ".tund.internal" }

// routeOfAddr reverses routeHost for DialContext ("host:port"); 0 = main.
func routeOfAddr(addr string) int {
	host, _, _ := strings.Cut(addr, ":")
	if n, ok := strings.CutPrefix(host, "route-"); ok {
		if i, err := strconv.Atoi(strings.TrimSuffix(n, ".tund.internal")); err == nil {
			return i
		}
	}
	return 0
}

func (r *tunnelRules) applyRequest(h http.Header) {
	if r == nil {
		return
	}
	for _, k := range r.reqRemove {
		h.Del(k)
	}
	for k, v := range r.reqSet {
		h[k] = slices.Clone(v)
	}
}

// applyResponse rewrites the local service's response headers. origin is the
// visitor's Origin header.
func (r *tunnelRules) applyResponse(h http.Header, origin string) {
	if r == nil {
		return
	}
	for _, k := range r.respRemove {
		h.Del(k)
	}
	for k, v := range r.respSet {
		h[k] = slices.Clone(v)
	}
	if c := r.cors; c != nil {
		for k := range h {
			if strings.HasPrefix(k, "Access-Control-") {
				delete(h, k)
			}
		}
		h.Add("Vary", "Origin")
		if allowed, ok := c.allow(origin); ok {
			h.Set("Access-Control-Allow-Origin", allowed)
			if c.credentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if c.expose != "" {
				h.Set("Access-Control-Expose-Headers", c.expose)
			}
		}
	}
}

func (c *corsRule) allow(origin string) (string, bool) {
	if origin == "" {
		return "", false
	}
	if n, ok := normalizeOrigin(origin); ok && n != "*" && c.origins[n] {
		return origin, true
	}
	if c.anyOrigin {
		return "*", true
	}
	return "", false
}

func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Origin") != "" && r.Header.Get("Access-Control-Request-Method") != ""
}

// preflight answers a CORS preflight request at the edge. Allowed or not,
// the browser decides from the headers; the local service never sees it.
func (c *corsRule) preflight(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Add("Vary", "Origin")
	h.Add("Vary", "Access-Control-Request-Method")
	h.Add("Vary", "Access-Control-Request-Headers")
	if allowed, ok := c.allow(r.Header.Get("Origin")); ok {
		h.Set("Access-Control-Allow-Origin", allowed)
		h.Set("Access-Control-Allow-Methods", c.methods)
		headers := c.headers
		if headers == "" {
			// Echo what the browser asks for, if it is a plain header list.
			req := r.Header.Get("Access-Control-Request-Headers")
			ok := len(req) <= 2048
			for _, name := range strings.Split(req, ",") {
				if name = strings.TrimSpace(name); name != "" && !validHeaderName(name) {
					ok = false
				}
			}
			if ok {
				headers = req
			}
		}
		if headers != "" {
			h.Set("Access-Control-Allow-Headers", headers)
		}
		if c.credentials {
			h.Set("Access-Control-Allow-Credentials", "true")
		}
		h.Set("Access-Control-Max-Age", c.maxAge)
	}
	w.WriteHeader(http.StatusNoContent)
}

// visitorLimiter is a token bucket per visitor IP: n requests per period,
// up to n at once.
type visitorLimiter struct {
	n      int
	per    time.Duration
	label  string // "100 requests per minute"
	mu     sync.Mutex
	byIP   map[string]*visitorBucket
	pruned time.Time

	rejected atomic.Int64 // since the last notice
	noticeAt atomic.Int64 // unix nanos of the last notice
}

type visitorBucket struct {
	lim  *rate.Limiter
	last time.Time
}

func newVisitorLimiter(n int, per time.Duration) *visitorLimiter {
	unit := map[time.Duration]string{time.Second: "second", time.Minute: "minute", time.Hour: "hour"}[per]
	label := fmt.Sprintf("%d %s per %s", n, pluralWord(int64(n), "request", "requests"), unit)
	return &visitorLimiter{n: n, per: per, label: label, byIP: map[string]*visitorBucket{}, pruned: time.Now()}
}

func (l *visitorLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.pruned) > time.Minute {
		// A bucket idle for a whole period is full again: forget it.
		for k, b := range l.byIP {
			if now.Sub(b.last) > l.per {
				delete(l.byIP, k)
			}
		}
		l.pruned = now
	}
	b := l.byIP[ip]
	if b == nil {
		if len(l.byIP) >= maxRateVisitors {
			return true
		}
		b = &visitorBucket{lim: rate.NewLimiter(rate.Limit(float64(l.n)/l.per.Seconds()), l.n)}
		l.byIP[ip] = b
	}
	b.last = now
	return b.lim.AllowN(now, 1)
}

// retryAfter is a polite Retry-After in seconds for a rejected visitor.
func (l *visitorLimiter) retryAfter() string {
	return strconv.Itoa(max(1, int((l.per/time.Duration(l.n)+time.Second-1)/time.Second)))
}

// noteRejected counts a rejected request and returns the count to report
// when a notice to the client is due.
func (l *visitorLimiter) noteRejected(now time.Time) int64 {
	l.rejected.Add(1)
	last := l.noticeAt.Load()
	if now.UnixNano()-last < int64(limitNoticeGap) || !l.noticeAt.CompareAndSwap(last, now.UnixNano()) {
		return 0
	}
	return l.rejected.Swap(0)
}

func (r *tunnelRules) limiter() *visitorLimiter {
	if r == nil {
		return nil
	}
	return r.limit
}

func (r *tunnelRules) corsRule() *corsRule {
	if r == nil {
		return nil
	}
	return r.cors
}

// rateLimited answers 429 when the visitor is over the tunnel's rate limit.
// Rejected requests are not recorded (a flood would fill the inspector);
// the client hears about them at most once a minute.
func (s *Server) rateLimited(w http.ResponseWriter, r *http.Request, t *Tunnel) bool {
	l := t.rules.limiter()
	if l == nil {
		return false
	}
	now := time.Now()
	if l.allow(clientIP(r), now) {
		return false
	}
	w.Header().Set("Retry-After", l.retryAfter())
	s.renderPage(w, r, http.StatusTooManyRequests, pageMessage, map[string]any{
		"Title":   "Too many requests",
		"Message": fmt.Sprintf("%s accepts at most %s from each visitor. Try again in a moment.", t.Hostname, l.label),
	})
	if n := l.noteRejected(now); n > 0 {
		t.session.ctrl.Send(protocol.Message{Type: protocol.TypeNotice, ID: t.BindID, Error: fmt.Sprintf(
			"%s: rate limit reached (%s per visitor), %d %s answered with 429 Too Many Requests", t.PublicURL, l.label, n, pluralWord(n, "request", "requests"))})
	}
	return true
}

// answerPreflight answers a CORS preflight at the edge and records it.
func (s *Server) answerPreflight(w http.ResponseWriter, r *http.Request, t *Tunnel) bool {
	c := t.rules.corsRule()
	if c == nil || !isPreflight(r) {
		return false
	}
	ex := &exchange{id: newUUID(), tunnel: t, in: r, start: time.Now()}
	c.preflight(w, r)
	ex.status, ex.respHeader, ex.ttfb = http.StatusNoContent, w.Header().Clone(), time.Since(ex.start)
	s.finishExchange(ex)
	return true
}

func pluralWord(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
