package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tund/internal/protocol"
)

type exchangeKey struct{}

// exchange tracks one proxied request while it is in flight.
type exchange struct {
	id       string
	tunnel   *Tunnel
	in       *http.Request // inbound request as the visitor sent it
	start    time.Time
	replayOf string

	reqBody  *captureReader
	respBody *captureReader

	ttfb       time.Duration
	status     int
	respHeader http.Header
	err        string
	recorded   atomic.Bool
	route      *tunnelRoute // the route that served the request, nil = the tunnel's address
}

// captureReader passes data through while keeping the first max bytes.
type captureReader struct {
	rc        io.ReadCloser
	max       int
	mu        sync.Mutex
	buf       bytes.Buffer
	n         int64
	truncated bool
}

func (c *captureReader) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if n > 0 {
		c.mu.Lock()
		c.n += int64(n)
		if room := c.max - c.buf.Len(); room > 0 {
			c.buf.Write(p[:min(n, room)])
			if n > room {
				c.truncated = true
			}
		} else {
			c.truncated = true
		}
		c.mu.Unlock()
	}
	return n, err
}

func (c *captureReader) Close() error { return c.rc.Close() }

func (c *captureReader) snapshot() ([]byte, int64, bool) {
	if c == nil {
		return nil, 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Clone(c.buf.Bytes()), c.n, c.truncated
}

func (s *Server) setupProxy(t *Tunnel) {
	as := t.session
	t.transport = &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			st, err := as.openStreamTo(ctx, t.BindID, routeOfAddr(addr))
			if err != nil {
				return nil, err
			}
			if t.meter == nil {
				return st, nil
			}
			return &meteredConn{Conn: st, srv: s, m: t.meter}, nil
		},
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       60 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
	}
	t.proxy = &httputil.ReverseProxy{
		Transport: t.transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = t.Hostname
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Proto", s.cfg.PublicScheme)
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
			hostHeader := t.HostHeader
			if route := t.rules.route(pr.In.URL.Path); route != nil {
				pr.Out.URL.Host = routeHost(route.index)
				route.stripPath(pr.Out.URL)
				hostHeader = route.hostHeader
				if ex, _ := pr.In.Context().Value(exchangeKey{}).(*exchange); ex != nil {
					ex.route = route
				}
			}
			if hostHeader != "" {
				pr.Out.Host = hostHeader
			} else {
				pr.Out.Host = pr.In.Host
			}
			stripAuthCookie(pr.Out.Header)
			pr.Out.Header.Del(protocol.HeaderSkipWarning)
			stripTundHeaders(pr.Out.Header)
			t.rules.applyRequest(pr.Out.Header)
			setIdentityHeaders(pr.Out.Header, identityFrom(pr.In.Context()))
		},
		ModifyResponse: func(resp *http.Response) error {
			ex, _ := resp.Request.Context().Value(exchangeKey{}).(*exchange)
			if ex == nil {
				return nil
			}
			t.rules.applyResponse(resp.Header, ex.in.Header.Get("Origin"))
			ex.ttfb = time.Since(ex.start)
			ex.status = resp.StatusCode
			ex.respHeader = resp.Header.Clone()
			if resp.StatusCode == http.StatusSwitchingProtocols {
				// Upgraded connections (WebSockets) can live for hours; record now.
				s.finishExchange(ex)
				return nil
			}
			if resp.Body != nil && resp.Body != http.NoBody {
				ex.respBody = &captureReader{rc: resp.Body, max: s.rt().CaptureMaxBody}
				resp.Body = ex.respBody
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			ex, _ := r.Context().Value(exchangeKey{}).(*exchange)
			local := t.LocalAddr
			if ex != nil && ex.route != nil {
				local = ex.route.localAddr
			}
			var le *protocol.LocalError
			switch {
			case errors.Is(err, context.Canceled):
				if ex != nil {
					ex.err = "client closed the request"
				}
				return
			case errors.As(err, &le):
				if ex != nil {
					ex.err = le.Msg
					ex.status = http.StatusBadGateway
				}
				s.renderPage(w, r, http.StatusBadGateway, pageBadGateway, map[string]any{
					"Host": t.Hostname, "Local": local, "Detail": le.Msg,
				})
			default:
				if ex != nil {
					ex.err = err.Error()
					ex.status = http.StatusBadGateway
				}
				s.renderPage(w, r, http.StatusBadGateway, pageBadGateway, map[string]any{
					"Host": t.Hostname, "Local": local, "Detail": err.Error(),
				})
			}
		},
	}
}

// serveTunnel proxies r through t and records the exchange.
func (s *Server) serveTunnel(w http.ResponseWriter, r *http.Request, t *Tunnel, replayOf string) string {
	ex := &exchange{id: newUUID(), tunnel: t, in: r, start: time.Now(), replayOf: replayOf}
	if r.Body != nil && r.Body != http.NoBody {
		ex.reqBody = &captureReader{rc: r.Body, max: s.rt().CaptureMaxBody}
		r.Body = ex.reqBody
	}
	in := r.Clone(context.WithValue(r.Context(), exchangeKey{}, ex))
	t.proxy.ServeHTTP(w, in)
	s.finishExchange(ex)
	return ex.id
}

func (s *Server) finishExchange(ex *exchange) {
	if !ex.recorded.CompareAndSwap(false, true) {
		return
	}
	t, r := ex.tunnel, ex.in
	rec := &RequestRecord{
		ID:          ex.id,
		TunnelID:    t.ID,
		UserID:      t.UserID,
		Hostname:    t.Hostname,
		Method:      r.Method,
		Path:        r.URL.RequestURI(),
		Proto:       r.Proto,
		RemoteAddr:  clientIP(r),
		ReqHeaders:  r.Header.Clone(),
		Status:      ex.status,
		RespHeaders: ex.respHeader,
		TTFB:        ex.ttfb,
		Duration:    time.Since(ex.start),
		Error:       ex.err,
		ReplayOf:    ex.replayOf,
		StartedAt:   ex.start,
	}
	stripAuthCookie(rec.ReqHeaders)
	rec.ReqHeaders.Del(protocol.HeaderSkipWarning)
	stripTundHeaders(rec.ReqHeaders)
	setIdentityHeaders(rec.ReqHeaders, identityFrom(r.Context()))
	rec.ReqBody, rec.ReqBodySize, rec.ReqBodyTruncated = ex.reqBody.snapshot()
	rec.RespBody, rec.RespBodySize, rec.RespBodyTruncated = ex.respBody.snapshot()
	s.recorder.Add(rec)
	s.maybeScan(t, rec)
	if t.meter != nil {
		t.meter.pendingReq.Add(1)
	}

	t.session.ctrl.Send(protocol.Message{Type: protocol.TypeRequest, ID: t.BindID, Request: &protocol.RequestEvent{
		RequestID:  rec.ID,
		Method:     rec.Method,
		Path:       rec.Path,
		Status:     rec.Status,
		DurationMS: ms(rec.Duration),
		RemoteAddr: rec.RemoteAddr,
		Error:      rec.Error,
	}})
}

// stripAuthCookie removes the edge's visitor cookies (auth, browser warning) so
// they never reach the local app.
func stripAuthCookie(h http.Header) {
	cookies := h.Values("Cookie")
	if len(cookies) == 0 {
		return
	}
	var kept []string
	for _, line := range cookies {
		var parts []string
		for _, c := range strings.Split(line, ";") {
			if name, _, _ := strings.Cut(strings.TrimSpace(c), "="); name == authCookieName || name == warnCookieName {
				continue
			}
			if c = strings.TrimSpace(c); c != "" {
				parts = append(parts, c)
			}
		}
		if len(parts) > 0 {
			kept = append(kept, strings.Join(parts, "; "))
		}
	}
	h.Del("Cookie")
	for _, k := range kept {
		h.Add("Cookie", k)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Recorder writes captured requests to the database in the background so
// proxying never waits on Postgres.
type Recorder struct {
	store *Store
	ch    chan *RequestRecord
	conns chan *ConnRecord
}

func NewRecorder(store *Store) *Recorder {
	return &Recorder{store: store, ch: make(chan *RequestRecord, 4096), conns: make(chan *ConnRecord, 4096)}
}

func (r *Recorder) AddConn(rec *ConnRecord) {
	select {
	case r.conns <- rec:
	default:
		logf("recorder queue full, dropping connection %s", rec.ID)
	}
}

func (r *Recorder) Add(rec *RequestRecord) {
	select {
	case r.ch <- rec:
	default:
		logf("recorder queue full, dropping request %s", rec.ID)
	}
}

func (r *Recorder) Run(ctx context.Context) {
	var batch []*RequestRecord
	var conns []*ConnRecord
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	flush := func() {
		fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if len(batch) > 0 {
			if err := r.store.InsertRequests(fctx, batch); err != nil {
				logf("store %d requests: %v", len(batch), err)
			}
			batch = batch[:0]
		}
		if len(conns) > 0 {
			if err := r.store.InsertConnections(fctx, conns); err != nil {
				logf("store %d connections: %v", len(conns), err)
			}
			conns = conns[:0]
		}
	}
	for {
		select {
		case <-ctx.Done():
			// Drain what is already queued.
			for {
				select {
				case rec := <-r.ch:
					batch = append(batch, rec)
				case c := <-r.conns:
					conns = append(conns, c)
				default:
					flush()
					return
				}
			}
		case rec := <-r.ch:
			batch = append(batch, rec)
			if len(batch) >= 100 {
				flush()
			}
		case c := <-r.conns:
			conns = append(conns, c)
			if len(conns) >= 100 {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}
