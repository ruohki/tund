package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"tund/internal/protocol"
	"tund/internal/tlsutil"
)

// TunnelSpec describes one tunnel to open.
type TunnelSpec struct {
	Name       string
	LocalAddr  string // normalized, see ParseLocalAddr
	Subdomain  string
	Hostname   string
	HostHeader string
	Auth       *protocol.Auth
	Random     bool // skip the account's default static hostname
	Pin        bool // keep the resulting hostname (or tcp port) as a static address
	// Proto is protocol.ProtoHTTP (default when empty), ProtoTCP or ProtoTLS.
	// For tcp and tls LocalAddr is host:port (see ParseTCPAddr).
	Proto         string
	RemotePort    int      // tcp: requested public port
	AllowIPs      []string // IPs/CIDRs allowed to connect; empty = everyone
	TerminateCert string   // tls: terminate TLS at the client with this cert…
	TerminateKey  string   // …and key (PEM files); otherwise passthrough
}

// Options configure a Client.
type Options struct {
	Server    string // base URL of the tund server (dashboard host)
	Authtoken string
	Tunnels   []TunnelSpec
	State     *State // optional, remembers random subdomains
	Display   *Display
	// Events, if set, replaces Display: nothing is printed and every status
	// change is delivered as an Event instead (see embed.go).
	Events func(Event)
}

// ErrStopped is returned (wrapped, see errors.Is) by Run when the server
// closed every tunnel, e.g. from the dashboard or the API.
var ErrStopped = errors.New("all tunnels were closed by the server")

type stoppedError struct{ msg string }

func (e *stoppedError) Error() string        { return e.msg }
func (e *stoppedError) Is(target error) bool { return target == ErrStopped }

// stopped builds the error for "every tunnel was closed", using the server's
// reason when all tunnels share one.
func (c *Client) stopped() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	reason, n := "", 0
	for _, t := range c.tunnels {
		if t.status != statusClosed {
			continue
		}
		if n > 0 && t.err != reason {
			return ErrStopped
		}
		reason = t.err
		n++
	}
	switch {
	case reason == "":
		return ErrStopped
	case n == 1:
		return &stoppedError{"tunnel closed: " + reason}
	}
	return &stoppedError{"all tunnels closed: " + reason}
}

// AuthError means the server rejected the authtoken (HTTP 401). The caller
// can log in again and retry.
type AuthError struct {
	Server string
	Msg    string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("%s rejected the authtoken: %s", e.Server, e.Msg)
}

type fatalError struct{ err error }

func (e *fatalError) Error() string { return e.err.Error() }
func (e *fatalError) Unwrap() error { return e.err }

type tunnelStatus int

const (
	statusPending tunnelStatus = iota
	statusOnline
	statusFailed
	statusClosed
)

type tunnel struct {
	spec   TunnelSpec
	target localTarget

	// Guarded by Client.mu.
	status   tunnelStatus
	label    string // remembered random subdomain; unused when the user picked the name
	port     int    // tcp: remembered random public port
	termCfg  *tls.Config
	remote   int // tcp: bound public port
	url      string
	host     string
	authMode string
	tunnelID string
	static   bool
	warnPage bool // browser visitors see the warning page first
	warning  string
	err      string
	retries  int
}

func (t *tunnel) userNamed() bool {
	return t.spec.Subdomain != "" || t.spec.Hostname != "" || t.spec.RemotePort != 0
}

func (t *tunnel) isTCP() bool { return t.spec.proto() == protocol.ProtoTCP }

// Client keeps tunnels open against a tund server, reconnecting as needed.
type Client struct {
	opts       Options
	ui         observer
	connectURL string
	httpClient *http.Client

	mu       sync.Mutex
	tunnels  []*tunnel
	byName   map[string]*tunnel
	sessions int // sessions that got as far as the welcome message
}

// New validates the options and prepares a client.
func New(opts Options) (*Client, error) {
	if opts.Authtoken == "" {
		return nil, errors.New("no authtoken configured")
	}
	if len(opts.Tunnels) == 0 {
		return nil, errors.New("no tunnels to start")
	}
	server, err := NormalizeServer(opts.Server)
	if err != nil {
		return nil, err
	}
	opts.Server = server
	wsURL, _ := ConnectURL(server)
	var ui observer
	switch {
	case opts.Events != nil:
		ui = eventObserver{opts.Events}
	case opts.Display != nil:
		ui = opts.Display
	default:
		ui = NewDisplay(true)
	}
	c := &Client{
		opts:       opts,
		ui:         ui,
		connectURL: wsURL,
		byName:     map[string]*tunnel{},
		httpClient: &http.Client{Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			TLSClientConfig:     tlsutil.MustClientConfig(),
			TLSHandshakeTimeout: 10 * time.Second,
			// WebSocket upgrades need HTTP/1.1.
			TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
		}},
	}
	for _, s := range opts.Tunnels {
		if s.Name == "" {
			return nil, errors.New("tunnel without a name")
		}
		if _, dup := c.byName[s.Name]; dup {
			return nil, fmt.Errorf("duplicate tunnel name %q", s.Name)
		}
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("tunnel %q: %w", s.Name, err)
		}
		if s.AllowIPs, err = NormalizeAllowIPs(s.AllowIPs); err != nil {
			return nil, fmt.Errorf("tunnel %q: %w", s.Name, err)
		}
		target, err := targetFor(s)
		if err != nil {
			return nil, err
		}
		t := &tunnel{spec: s, target: target}
		if t.termCfg, err = terminationConfig(s.TerminateCert, s.TerminateKey); err != nil {
			return nil, fmt.Errorf("tunnel %q: %w", s.Name, err)
		}
		if !t.userNamed() {
			if t.isTCP() {
				t.port = opts.State.GetPort(c.stateKey(t))
			} else {
				t.label = opts.State.Get(c.stateKey(t))
			}
		}
		c.tunnels = append(c.tunnels, t)
		c.byName[s.Name] = t
	}
	names := make([]string, len(c.tunnels))
	for i, t := range c.tunnels {
		names[i] = t.spec.Name
	}
	c.ui.setTunnelNames(names)
	return c, nil
}

func (c *Client) stateKey(t *tunnel) string {
	if p := t.spec.proto(); p != protocol.ProtoHTTP {
		return c.opts.Server + " " + p + " " + t.spec.LocalAddr
	}
	return c.opts.Server + " " + t.spec.LocalAddr
}

// Run connects and keeps the tunnels online until ctx is cancelled or a fatal
// error happens (bad authtoken, no tunnel could be bound, …).
func (c *Client) Run(ctx context.Context) error {
	attempt := 0
	for {
		connected, err := c.runSession(ctx)
		if ctx.Err() != nil {
			c.ui.Stopped()
			return nil
		}
		var fe *fatalError
		if errors.As(err, &fe) {
			return fe.err
		}
		if connected {
			attempt = 0
		}
		delay := backoff(attempt)
		attempt++
		c.ui.Reconnecting(err, delay)
		select {
		case <-ctx.Done():
			c.ui.Stopped()
			return nil
		case <-time.After(delay):
		}
	}
}

func backoff(attempt int) time.Duration {
	d := time.Second << min(attempt, 5)
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	jitter := time.Duration(rand.Int64N(int64(d) / 5))
	return d - d/10 + jitter
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	hostname, _ := os.Hostname()
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+c.opts.Authtoken)
	hdr.Set("User-Agent", "tund/"+Version)
	hdr.Set(protocol.HeaderVersion, protocol.Version)
	hdr.Set(protocol.HeaderClient, Version)
	hdr.Set(protocol.HeaderOS, runtime.GOOS+"/"+runtime.GOARCH)
	hdr.Set(protocol.HeaderHostname, hostname)

	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(dctx, c.connectURL, &websocket.DialOptions{
		HTTPClient: c.httpClient,
		HTTPHeader: hdr,
	})
	if err != nil {
		if resp != nil {
			return nil, c.handshakeError(resp, err)
		}
		return nil, fmt.Errorf("cannot reach %s: %w", c.opts.Server, unwrapDial(err))
	}
	// The connection must outlive ctx (Ctrl+C) long enough to send unbinds;
	// runSession closes it explicitly.
	return websocket.NetConn(context.WithoutCancel(ctx), ws, websocket.MessageBinary), nil
}

func (c *Client) handshakeError(resp *http.Response, err error) error {
	msg := ""
	if resp.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			msg = e.Error
		} else {
			msg = strings.TrimSpace(string(b))
		}
	}
	if msg == "" {
		msg = resp.Status
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return &fatalError{&AuthError{Server: c.opts.Server, Msg: msg}}
	case http.StatusForbidden:
		// e.g. a disabled account: logging in again would not help.
		return &fatalError{errors.New(msg)}
	case http.StatusUpgradeRequired:
		return &fatalError{fmt.Errorf("this client (tund %s) is not compatible with the server: %s", Version, msg)}
	case http.StatusNotFound:
		if c.sessions == 0 {
			return &fatalError{fmt.Errorf("%s does not look like a tund server (got 404 for %s); check `tund config set-server`", c.opts.Server, protocol.ConnectPath)}
		}
	}
	return fmt.Errorf("server refused the connection: %s", msg)
}

// unwrapDial drops the long "failed to WebSocket dial: failed to send handshake request: Get …" prefix.
func unwrapDial(err error) error {
	var op *net.OpError
	if errors.As(err, &op) {
		return op
	}
	return err
}

// runSession runs one connection. connected reports whether the server
// accepted us (so backoff can reset).
func (c *Client) runSession(ctx context.Context) (connected bool, err error) {
	c.ui.Connecting(c.opts.Server)
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	nc, err := c.dial(sctx)
	if err != nil {
		return false, err
	}
	sess, err := yamux.Client(nc, protocol.YamuxConfig())
	if err != nil {
		nc.Close()
		return false, err
	}
	defer sess.Close()

	st, err := sess.OpenStream()
	if err != nil {
		return false, err
	}
	ctl := protocol.NewControl(st)
	_ = st.SetReadDeadline(time.Now().Add(15 * time.Second))
	m, err := ctl.Recv()
	if err != nil {
		return false, fmt.Errorf("waiting for the server: %w", err)
	}
	_ = st.SetReadDeadline(time.Time{})
	switch m.Type {
	case protocol.TypeWelcome:
	case protocol.TypeError:
		return false, fmt.Errorf("server: %s", m.Error)
	default:
		return false, fmt.Errorf("unexpected %q message from server", m.Type)
	}

	first := c.sessions == 0
	c.sessions++
	w := welcome{account: m.Account, serverVersion: m.ServerVersion, dashboardURL: strings.TrimRight(m.DashboardURL, "/")}
	if w.dashboardURL == "" {
		w.dashboardURL = c.opts.Server
	}
	c.ui.Online(w)

	msgs := make(chan protocol.Message)
	readErr := make(chan error, 1)
	go func() {
		for {
			m, err := ctl.Recv()
			if err != nil {
				readErr <- err
				return
			}
			select {
			case msgs <- m:
			case <-sctx.Done():
				return
			}
		}
	}()
	go c.acceptStreams(sess)

	retry := make(chan *tunnel)
	scheduleRetry := func(t *tunnel) {
		time.AfterFunc(2*time.Second, func() {
			select {
			case retry <- t:
			case <-sctx.Done():
			}
		})
	}

	c.mu.Lock()
	var toBind []*tunnel
	for _, t := range c.tunnels {
		if t.status == statusFailed || t.status == statusClosed {
			continue
		}
		t.status, t.retries, t.url, t.host, t.warning = statusPending, 0, "", "", ""
		toBind = append(toBind, t)
	}
	c.mu.Unlock()
	for _, t := range toBind {
		if err := c.sendBind(ctl, t); err != nil {
			return true, err
		}
	}

	headerShown := false
	serverErr := ""
	for {
		select {
		case <-ctx.Done():
			c.shutdown(ctl)
			return true, nil
		case err := <-readErr:
			if serverErr != "" {
				return true, errors.New(serverErr)
			}
			if errors.Is(err, io.EOF) {
				return true, errors.New("connection closed by server")
			}
			return true, fmt.Errorf("connection lost: %w", err)
		case <-sess.CloseChan():
			if serverErr != "" {
				return true, errors.New(serverErr)
			}
			return true, errors.New("connection lost")
		case t := <-retry:
			c.mu.Lock()
			pending := t.status == statusPending
			c.mu.Unlock()
			if pending {
				if err := c.sendBind(ctl, t); err != nil {
					return true, err
				}
			}
			continue
		case m := <-msgs:
			t := c.byName[m.ID]
			switch m.Type {
			case protocol.TypeBound:
				if t == nil {
					continue
				}
				c.mu.Lock()
				t.status, t.url, t.host, t.authMode, t.err = statusOnline, m.URL, hostOf(m.URL), m.AuthMode, ""
				t.static, t.warning, t.tunnelID, t.warnPage = m.Static, m.Warning, m.TunnelID, m.BrowserWarning
				// Remember throwaway labels only: static hostnames are resolved
				// by the server, and remembering one would steer --random to it.
				t.remote = m.RemotePort
				remember := !t.userNamed() && !m.Static
				if remember {
					if t.isTCP() {
						t.port = m.RemotePort
					} else {
						t.label = firstLabel(t.host)
					}
				}
				v := c.view(t)
				c.mu.Unlock()
				if remember {
					var err error
					if t.isTCP() {
						err = c.opts.State.SetPort(c.stateKey(t), t.port)
					} else {
						err = c.opts.State.Set(c.stateKey(t), t.label)
					}
					if err != nil {
						c.ui.Warn("could not save state: " + err.Error())
					}
				}
				if headerShown {
					c.ui.TunnelOnline(v)
				}
			case protocol.TypeBindError:
				if t == nil {
					continue
				}
				c.mu.Lock()
				switch {
				case !first && t.retries < 5:
					// After a reconnect the server may still hold our old
					// session for a moment; give it time to let go.
					t.retries++
					c.mu.Unlock()
					scheduleRetry(t)
					continue
				case !t.userNamed() && (t.label != "" || t.port != 0):
					// The remembered random subdomain/port is gone; ask for a new one.
					t.label, t.port, t.retries = "", 0, 0
					c.mu.Unlock()
					if t.isTCP() {
						_ = c.opts.State.SetPort(c.stateKey(t), 0)
					} else {
						_ = c.opts.State.Set(c.stateKey(t), "")
					}
					if err := c.sendBind(ctl, t); err != nil {
						return true, err
					}
					continue
				}
				t.status, t.err = statusFailed, m.Error
				v := c.view(t)
				c.mu.Unlock()
				if headerShown {
					c.ui.TunnelFailed(v)
				}
			case protocol.TypeRequest:
				if t != nil && m.Request != nil {
					c.mu.Lock()
					v := c.view(t)
					c.mu.Unlock()
					c.ui.Request(v, *m.Request)
				}
			case protocol.TypeConnection:
				if t != nil && m.Conn != nil {
					c.mu.Lock()
					v := c.view(t)
					c.mu.Unlock()
					c.ui.Connection(v, *m.Conn)
				}
			case protocol.TypeClosed:
				if t == nil {
					continue
				}
				c.mu.Lock()
				t.status, t.err = statusClosed, m.Error
				v := c.view(t)
				c.mu.Unlock()
				c.ui.TunnelClosed(v)
			case protocol.TypeError:
				// The server closes the connection after an error; report it
				// as the reason instead of a generic "connection lost".
				serverErr = "server: " + m.Error
			case protocol.TypeNotice:
				// Informational (e.g. transfer quota used up); the session stays up.
				switch {
				case m.UpdateVersion != "":
					c.ui.UpdateAvailable(m.UpdateVersion)
				case m.Error != "":
					c.ui.Warn(m.Error)
				}
			}
		}

		online, pending, failed, closed := c.counts()
		if pending == 0 && online == 0 {
			if !headerShown {
				c.ui.Header(w, c.views())
			}
			switch {
			case closed > 0 && failed == 0:
				return true, &fatalError{c.stopped()}
			case closed == 0:
				return true, &fatalError{errors.New("no tunnel could be started")}
			default:
				return true, &fatalError{errors.New("no tunnels left")}
			}
		}
		if !headerShown && pending == 0 {
			c.ui.Header(w, c.views())
			headerShown = true
		}
	}
}

func (c *Client) sendBind(ctl *protocol.Control, t *tunnel) error {
	c.mu.Lock()
	b := &protocol.Bind{
		Name:       t.spec.Name,
		LocalAddr:  t.spec.LocalAddr,
		Subdomain:  t.spec.Subdomain,
		Hostname:   t.spec.Hostname,
		HostHeader: t.spec.HostHeader,
		Auth:       t.spec.Auth,
		Random:     t.spec.Random,
		Pin:        t.spec.Pin,
		RemotePort: t.spec.RemotePort,
		AllowIPs:   t.spec.AllowIPs,
	}
	if p := t.spec.proto(); p != protocol.ProtoHTTP {
		b.Proto = p
	}
	switch {
	case t.userNamed():
	case t.isTCP() && t.port != 0:
		// A preference only: reserved ports of the account win.
		b.RemotePort, b.Auto = t.port, true
	case !t.isTCP() && t.label != "":
		// A preference only: the server prefers the account's default
		// static hostname unless Random is set.
		b.Subdomain, b.Auto = t.label, true
	}
	c.mu.Unlock()
	return ctl.Send(protocol.Message{Type: protocol.TypeBind, ID: t.spec.Name, Bind: b})
}

// shutdown unbinds everything so the server frees the hostnames right away.
func (c *Client) shutdown(ctl *protocol.Control) {
	c.mu.Lock()
	var ids []string
	for _, t := range c.tunnels {
		if t.status == statusOnline || t.status == statusPending {
			ids = append(ids, t.spec.Name)
		}
	}
	c.mu.Unlock()
	done := make(chan struct{})
	go func() {
		for _, id := range ids {
			if ctl.Send(protocol.Message{Type: protocol.TypeUnbind, ID: id}) != nil {
				break
			}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

func (c *Client) counts() (online, pending, failed, closed int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.tunnels {
		switch t.status {
		case statusOnline:
			online++
		case statusPending:
			pending++
		case statusFailed:
			failed++
		case statusClosed:
			closed++
		}
	}
	return
}

// tunnelView is an immutable snapshot of a tunnel for the display.
type tunnelView struct {
	Name, URL, Host, Local, AuthMode, Err, Warning, TunnelID, Proto string
	RemotePort                                                      int
	Terminated                                                      bool // tls: terminated at the client
	Online, Failed, Closed, Static, BrowserWarning                  bool
}

func (c *Client) view(t *tunnel) tunnelView {
	return tunnelView{
		Name: t.spec.Name, URL: t.url, Host: t.host, Local: t.spec.LocalAddr, AuthMode: t.authMode, Err: t.err,
		Warning: t.warning, Static: t.static, TunnelID: t.tunnelID, BrowserWarning: t.warnPage,
		Proto: t.spec.proto(), RemotePort: t.remote, Terminated: t.termCfg != nil,
		Online: t.status == statusOnline, Failed: t.status == statusFailed, Closed: t.status == statusClosed,
	}
}

func (c *Client) views() []tunnelView {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]tunnelView, 0, len(c.tunnels))
	for _, t := range c.tunnels {
		out = append(out, c.view(t))
	}
	return out
}

func (c *Client) acceptStreams(sess *yamux.Session) {
	for {
		s, err := sess.AcceptStream()
		if err != nil {
			return
		}
		go c.handleStream(s)
	}
}

func (c *Client) handleStream(s *yamux.Stream) {
	_ = s.SetReadDeadline(time.Now().Add(15 * time.Second))
	h, err := protocol.ReadStreamHeader(s)
	if err != nil {
		s.Close()
		return
	}
	_ = s.SetReadDeadline(time.Time{})

	t := c.byName[h.Tunnel]
	active := false
	if t != nil {
		c.mu.Lock()
		active = t.status == statusOnline || t.status == statusPending
		c.mu.Unlock()
	}
	if !active {
		_ = protocol.WriteStreamError(s, fmt.Sprintf("tunnel %q is not active on this client", h.Tunnel))
		s.Close()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	local, err := t.target.dial(ctx)
	cancel()
	if err != nil {
		_ = protocol.WriteStreamError(s, fmt.Sprintf("could not connect to %s (%s)", t.spec.LocalAddr, friendlyDialError(err)))
		s.Close()
		return
	}
	if err := protocol.WriteStreamOK(s); err != nil {
		local.Close()
		s.Close()
		return
	}
	if t.termCfg != nil {
		// Terminate the visitor's TLS here and forward plaintext.
		tc := tls.Server(s, t.termCfg)
		hctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := tc.HandshakeContext(hctx)
		cancel()
		if err != nil {
			tc.Close()
			local.Close()
			return
		}
		pipe(tc, local)
		return
	}
	pipe(s, local)
}

// pipe copies both directions, propagating half-closes. Once one direction
// has finished, the other gets a grace period before both are torn down.
func pipe(stream, local net.Conn) {
	var once sync.Once
	closeBoth := func() {
		stream.Close()
		local.Close()
	}
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		if _, err := io.Copy(dst, src); err != nil {
			once.Do(closeBoth)
		} else {
			closeWrite(dst)
		}
		done <- struct{}{}
	}
	go cp(local, stream)
	go cp(stream, local)
	<-done
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
	}
	once.Do(closeBoth)
}

func closeWrite(c net.Conn) {
	switch v := c.(type) {
	case *yamux.Stream:
		_ = v.Close() // yamux Close only closes our sending side
	case *tls.Conn:
		_ = v.CloseWrite() // close_notify
		closeWrite(v.NetConn())
	case interface{ CloseWrite() error }:
		_ = v.CloseWrite()
	}
}
