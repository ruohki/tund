package client

import (
	"context"
	"errors"
	"sync"
	"time"

	"tund/internal/protocol"
)

// observer receives status changes from a Client. *Display renders them on
// the terminal; eventObserver turns them into Events for library use.
type observer interface {
	setTunnelNames([]string)
	Connecting(server string)
	Online(w welcome)
	Reconnecting(err error, delay time.Duration)
	Stopped()
	Warn(msg string)
	UpdateAvailable(version string)
	Header(w welcome, ts []tunnelView)
	TunnelOnline(t tunnelView)
	TunnelFailed(t tunnelView)
	TunnelClosed(t tunnelView)
	Request(t tunnelView, ev protocol.RequestEvent)
	Connection(t tunnelView, ev protocol.ConnEvent)
}

// EventKind says what an Event reports.
type EventKind string

const (
	EventConnecting   EventKind = "connecting"   // dialing the server
	EventConnected    EventKind = "connected"    // session accepted (Account, DashboardURL)
	EventBound        EventKind = "bound"        // tunnel online (again, after a reconnect)
	EventFailed       EventKind = "failed"       // the server refused the tunnel (Error)
	EventClosed       EventKind = "closed"       // stopped from the dashboard / API (Error)
	EventRequest      EventKind = "request"      // a request went through (Request)
	EventConnection   EventKind = "connection"   // a tcp/tls connection finished (Conn)
	EventReconnecting EventKind = "reconnecting" // connection lost (Error, Delay)
	EventWarning      EventKind = "warning"      // non-fatal problem (Error)
	EventStopped      EventKind = "stopped"      // Run returned after cancellation
)

// Event is a status change of a Client running with Options.Events.
type Event struct {
	Kind      EventKind
	Tunnel    string // tunnel name
	TunnelID  string // server-side tunnel id (changes on reconnect)
	URL       string
	Hostname  string
	LocalAddr string
	AuthMode  string
	Static    bool
	// BrowserWarning: browser visitors see a warning page first (skip it
	// with the protocol.HeaderSkipWarning request header).
	BrowserWarning bool
	Warning        string
	Error          string
	Account        string
	DashboardURL   string
	Delay          time.Duration
	Request        *protocol.RequestEvent
	Conn           *protocol.ConnEvent
	// Proto and RemotePort describe tcp/tls tunnels.
	Proto      string
	RemotePort int
}

type eventObserver struct{ fn func(Event) }

func (o eventObserver) tunnelEvent(kind EventKind, t tunnelView) Event {
	return Event{Kind: kind, Tunnel: t.Name, TunnelID: t.TunnelID, URL: t.URL, Hostname: t.Host, LocalAddr: t.Local,
		AuthMode: t.AuthMode, Static: t.Static, Warning: t.Warning, Error: t.Err, BrowserWarning: t.BrowserWarning,
		Proto: t.Proto, RemotePort: t.RemotePort}
}

func (o eventObserver) setTunnelNames([]string) {}
func (o eventObserver) Connecting(server string) {
	o.fn(Event{Kind: EventConnecting, URL: server})
}
func (o eventObserver) Online(w welcome) {
	o.fn(Event{Kind: EventConnected, Account: w.account, DashboardURL: w.dashboardURL})
}
func (o eventObserver) Reconnecting(err error, delay time.Duration) {
	e := Event{Kind: EventReconnecting, Delay: delay}
	if err != nil {
		e.Error = err.Error()
	}
	o.fn(e)
}
func (o eventObserver) Stopped()                  { o.fn(Event{Kind: EventStopped}) }
func (o eventObserver) Warn(msg string)           { o.fn(Event{Kind: EventWarning, Error: msg}) }
func (o eventObserver) TunnelOnline(t tunnelView) { o.fn(o.tunnelEvent(EventBound, t)) }
func (o eventObserver) TunnelFailed(t tunnelView) { o.fn(o.tunnelEvent(EventFailed, t)) }
func (o eventObserver) TunnelClosed(t tunnelView) { o.fn(o.tunnelEvent(EventClosed, t)) }
func (o eventObserver) UpdateAvailable(v string) {
	o.Warn("tund " + v + " is available (this is " + Version + "); update with: tund update")
}
func (o eventObserver) Header(w welcome, ts []tunnelView) {
	for _, t := range ts {
		switch {
		case t.Online:
			o.TunnelOnline(t)
		case t.Failed:
			o.TunnelFailed(t)
		}
	}
}
func (o eventObserver) Request(t tunnelView, ev protocol.RequestEvent) {
	e := o.tunnelEvent(EventRequest, t)
	e.Request = &ev
	o.fn(e)
}
func (o eventObserver) Connection(t tunnelView, ev protocol.ConnEvent) {
	e := o.tunnelEvent(EventConnection, t)
	e.Conn = &ev
	o.fn(e)
}

// TunnelState is the lifecycle state of an embedded Tunnel.
type TunnelState string

const (
	StateConnecting   TunnelState = "connecting"
	StateOnline       TunnelState = "online"
	StateReconnecting TunnelState = "reconnecting"
	StateFailed       TunnelState = "failed"  // refused by the server or fatal error
	StateClosed       TunnelState = "closed"  // stopped from the dashboard / API
	StateStopped      TunnelState = "stopped" // Stop was called
)

// TunnelInfo is a snapshot of an embedded Tunnel.
type TunnelInfo struct {
	Name           string
	LocalAddr      string
	State          TunnelState
	TunnelID       string
	URL            string
	Hostname       string
	AuthMode       string
	Static         bool
	BrowserWarning bool
	Warning        string
	Error          string
	Account        string
	DashboardURL   string
	Requests       int
	Connections    int
	Proto          string
	RemotePort     int
	LastRequestAt  time.Time
	StartedAt      time.Time
}

// TunnelOptions configure StartTunnel.
type TunnelOptions struct {
	Server    string
	Authtoken string
	Spec      TunnelSpec
	State     *State      // optional, remembers random labels
	OnEvent   func(Event) // optional; called synchronously, must not block
}

// Tunnel runs a single tunnel in the background on its own connection,
// reconnecting like the CLI. It never writes to the terminal.
type Tunnel struct {
	cancel  context.CancelFunc
	done    chan struct{}
	ready   chan struct{} // closed on the first bound, failure or exit
	onEvent func(Event)

	mu        sync.Mutex
	info      TunnelInfo
	err       error
	readyOnce sync.Once
}

// StartTunnel validates the options and starts the tunnel in the background.
func StartTunnel(opts TunnelOptions) (*Tunnel, error) {
	t := &Tunnel{
		done:    make(chan struct{}),
		ready:   make(chan struct{}),
		onEvent: opts.OnEvent,
		info: TunnelInfo{Name: opts.Spec.Name, LocalAddr: opts.Spec.LocalAddr, State: StateConnecting, StartedAt: time.Now(),
			Proto: opts.Spec.proto(), RemotePort: opts.Spec.RemotePort},
	}
	c, err := New(Options{
		Server:    opts.Server,
		Authtoken: opts.Authtoken,
		Tunnels:   []TunnelSpec{opts.Spec},
		State:     opts.State,
		Events:    t.handle,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	go func() {
		err := c.Run(ctx)
		t.mu.Lock()
		switch {
		case ctx.Err() != nil:
			t.info.State = StateStopped
		case errors.Is(err, ErrStopped):
			t.info.State = StateClosed
		case err != nil:
			t.info.State = StateFailed
			if t.info.Error == "" {
				t.info.Error = err.Error()
			}
			t.err = err
		}
		t.mu.Unlock()
		t.markReady()
		close(t.done)
	}()
	return t, nil
}

func (t *Tunnel) markReady() { t.readyOnce.Do(func() { close(t.ready) }) }

func (t *Tunnel) handle(e Event) {
	t.mu.Lock()
	in := &t.info
	switch e.Kind {
	case EventConnecting:
		if in.State != StateConnecting {
			in.State = StateReconnecting
		}
	case EventConnected:
		in.Account, in.DashboardURL = e.Account, e.DashboardURL
	case EventBound:
		in.State, in.TunnelID, in.URL, in.Hostname = StateOnline, e.TunnelID, e.URL, e.Hostname
		in.AuthMode, in.Static, in.Warning, in.Error = e.AuthMode, e.Static, e.Warning, ""
		in.BrowserWarning = e.BrowserWarning
		in.RemotePort = e.RemotePort
	case EventFailed:
		in.State, in.Error = StateFailed, e.Error
	case EventClosed:
		in.State, in.Error = StateClosed, e.Error
	case EventReconnecting:
		in.State, in.Error = StateReconnecting, e.Error
	case EventRequest:
		in.Requests++
		in.LastRequestAt = time.Now()
	case EventConnection:
		in.Connections++
		in.LastRequestAt = time.Now()
	}
	t.mu.Unlock()
	if e.Kind == EventBound || e.Kind == EventFailed || e.Kind == EventClosed {
		t.markReady()
	}
	if t.onEvent != nil {
		t.onEvent(e)
	}
}

// Info returns the current state.
func (t *Tunnel) Info() TunnelInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.info
}

// WaitReady blocks until the tunnel is bound for the first time, has failed,
// or ctx ends. On failure it returns the server's reason.
func (t *Tunnel) WaitReady(ctx context.Context) (TunnelInfo, error) {
	select {
	case <-ctx.Done():
		info := t.Info()
		if info.Error != "" {
			return info, errors.New("tunnel is not online yet: " + info.Error)
		}
		return info, errors.New("tunnel is not online yet: timed out waiting for the server")
	case <-t.ready:
	}
	info := t.Info()
	switch info.State {
	case StateOnline:
		return info, nil
	case StateClosed:
		if info.Error != "" {
			return info, errors.New("tunnel closed: " + info.Error)
		}
		return info, errors.New("tunnel was closed by the server")
	}
	if info.Error != "" {
		return info, errors.New(info.Error)
	}
	if err := t.Err(); err != nil {
		return info, err
	}
	return info, errors.New("tunnel stopped")
}

// Stop closes the tunnel (the server frees the hostname right away) and waits
// for the connection to shut down.
func (t *Tunnel) Stop() {
	t.cancel()
	select {
	case <-t.done:
	case <-time.After(5 * time.Second):
	}
}

// Done is closed when the tunnel has stopped for good.
func (t *Tunnel) Done() <-chan struct{} { return t.done }

// Err is the fatal error that ended the tunnel, if any (e.g. *AuthError).
func (t *Tunnel) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}
