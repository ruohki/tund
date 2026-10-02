// Package protocol defines the wire format between the tund client and tund-server.
// See docs/SPEC.md for the full description.
package protocol

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Version is the protocol version exchanged in the X-Tund-Version header.
const Version = "1"

const (
	ConnectPath = "/_tund/ws"

	HeaderVersion  = "X-Tund-Version"
	HeaderOS       = "X-Tund-OS"
	HeaderHostname = "X-Tund-Hostname"
	HeaderClient   = "X-Tund-Client-Version"

	// HeaderSkipWarning on a visitor's request bypasses the browser warning page.
	HeaderSkipWarning = "Tund-Skip-Browser-Warning"
)

// Message types on the control stream.
const (
	TypeWelcome   = "welcome"
	TypeBind      = "bind"
	TypeBound     = "bound"
	TypeBindError = "bind_error"
	TypeUnbind    = "unbind"
	TypeRequest   = "request"
	TypeClosed    = "closed"
	TypeError     = "error"
	// TypeConnection reports a finished TCP/TLS connection (for the CLI log).
	TypeConnection = "connection"
	// TypeNotice is an informational message for the user (Error holds the
	// text), e.g. an exhausted transfer quota. The session stays up. With
	// UpdateVersion set it announces a newer client release; clients that know
	// the field show their own hint, older ones print Error.
	TypeNotice = "notice"
)

// CodeLifetime marks a tunnel closed (or about to be closed) because it
// reached the maximum tunnel lifetime; the client may bind it again.
const CodeLifetime = "lifetime"

// Tunnel protocols.
const (
	ProtoHTTP = "http" // default: HTTP(S) terminated at the edge, requests inspected
	ProtoTCP  = "tcp"  // raw TCP on a public port
	ProtoTLS  = "tls"  // TLS passthrough routed by SNI; the edge never decrypts
)

// Access policy modes.
const (
	AuthNone     = "none"
	AuthPassword = "password"
	AuthOIDC     = "oidc"
)

// Message is one newline-delimited JSON object on the control stream.
type Message struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`

	// welcome
	SessionID     string `json:"session_id,omitempty"`
	Account       string `json:"account,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`
	DashboardURL  string `json:"dashboard_url,omitempty"`

	// bind
	Bind *Bind `json:"bind,omitempty"`

	// bound
	TunnelID string `json:"tunnel_id,omitempty"`
	URL      string `json:"url,omitempty"`
	AuthMode string `json:"auth_mode,omitempty"`
	Static   bool   `json:"static,omitempty"` // hostname is pinned to the account
	// BrowserWarning: browser visitors see a warning page before the site
	// (skippable with the Tund-Skip-Browser-Warning request header).
	BrowserWarning bool   `json:"browser_warning,omitempty"`
	Warning        string `json:"warning,omitempty"` // bound, but something asked for did not happen (e.g. pin limit)
	// ExpiresAt: the server closes the tunnel at this time (maximum tunnel
	// lifetime). Also set on the notice that announces it.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// request
	Request *RequestEvent `json:"request,omitempty"`

	// connection
	Conn *ConnEvent `json:"conn,omitempty"`

	// bound (tcp): the public port
	RemotePort int `json:"remote_port,omitempty"`

	// bind_error / closed / error
	Error string `json:"error,omitempty"`
	// closed / notice: why, for clients that react to it (CodeLifetime).
	Code string `json:"code,omitempty"`

	// notice: the newest client release, when this client is older
	UpdateVersion string `json:"update_version,omitempty"`
}

// Bind asks the server to route a public hostname (or TCP port) to a local address.
type Bind struct {
	Name string `json:"name"`
	// Proto is ProtoHTTP (default when empty), ProtoTCP or ProtoTLS.
	Proto string `json:"proto,omitempty"`
	// RemotePort requests a public TCP port (tcp only); with Auto it is only a
	// preference (the port this client had last time).
	RemotePort int `json:"remote_port,omitempty"`
	// AllowIPs restricts who may connect: IPs or CIDRs. Empty = everyone.
	AllowIPs  []string `json:"allow_ips,omitempty"`
	Subdomain string   `json:"subdomain,omitempty"`
	// Auto marks Subdomain as a remembered preference (the label this client
	// got last time) rather than an explicit request: the account's default
	// static hostname wins over it, and a taken label silently falls back.
	Auto bool `json:"auto,omitempty"`
	// Random skips the default static hostname and asks for a throwaway one.
	Random bool `json:"random,omitempty"`
	// Pin makes the resulting hostname a static hostname of the account.
	Pin        bool   `json:"pin,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	LocalAddr  string `json:"local_addr"`
	HostHeader string `json:"host_header,omitempty"`
	Auth       *Auth  `json:"auth,omitempty"`
	// Rules are applied by the edge (http only).
	Rules *Rules `json:"rules,omitempty"`
}

// Rules shape the traffic of an HTTP tunnel at the edge.
type Rules struct {
	// RequestHeaders change what the local service receives,
	// ResponseHeaders what visitors receive.
	RequestHeaders  *HeaderRules `json:"request_headers,omitempty"`
	ResponseHeaders *HeaderRules `json:"response_headers,omitempty"`
	CORS            *CORS        `json:"cors,omitempty"`
	// RateLimit caps requests per visitor IP, e.g. "100/m" (per s, m or h).
	RateLimit string `json:"rate_limit,omitempty"`
	// Routes send path prefixes to other local addresses; the longest
	// matching prefix wins, everything else goes to Bind.LocalAddr.
	Routes []Route `json:"routes,omitempty"`
}

// HeaderRules remove headers, then set (replace) others.
type HeaderRules struct {
	Set    map[string]string `json:"set,omitempty"`
	Remove []string          `json:"remove,omitempty"`
}

// CORS makes the edge answer preflight requests and add the
// Access-Control-* headers to responses (replacing the local service's).
type CORS struct {
	Origins []string `json:"origins"`           // exact origins or "*"
	Methods []string `json:"methods,omitempty"` // default: the common methods
	Headers []string `json:"headers,omitempty"` // allowed request headers; default: whatever the browser asks for
	Expose  []string `json:"expose,omitempty"`  // response headers scripts may read
	// Credentials allows cookies; not possible with the "*" origin.
	Credentials bool `json:"credentials,omitempty"`
	MaxAge      int  `json:"max_age,omitempty"` // seconds browsers cache a preflight; default 600
}

// Route forwards requests under a path prefix to another local address.
type Route struct {
	Path      string `json:"path"`       // "/api" matches /api and /api/…
	LocalAddr string `json:"local_addr"` // like Bind.LocalAddr
	// StripPrefix removes Path before forwarding (/api/users → /users).
	StripPrefix bool `json:"strip_prefix,omitempty"`
}

// Auth is an access policy requested by the client. It overrides the policy
// stored on a reserved domain.
type Auth struct {
	Mode     string   `json:"mode"`
	Password string   `json:"password,omitempty"`
	Provider string   `json:"provider,omitempty"`
	Allow    []string `json:"allow,omitempty"`
}

// RequestEvent is a short summary of a proxied request, shown in the CLI.
type RequestEvent struct {
	RequestID  string  `json:"request_id"`
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	Status     int     `json:"status"`
	DurationMS float64 `json:"duration_ms"`
	RemoteAddr string  `json:"remote_addr,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// Control wraps the control stream with a JSON encoder/decoder. Send is safe
// for concurrent use; Recv must be called from a single goroutine.
type Control struct {
	mu  sync.Mutex
	enc *json.Encoder
	dec *json.Decoder
	c   io.Closer
}

func NewControl(rw io.ReadWriteCloser) *Control {
	return &Control{enc: json.NewEncoder(rw), dec: json.NewDecoder(bufio.NewReader(rw)), c: rw}
}

func (c *Control) Send(m Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(m)
}

func (c *Control) Recv() (Message, error) {
	var m Message
	err := c.dec.Decode(&m)
	return m, err
}

func (c *Control) Close() error { return c.c.Close() }

// StreamHeader is written by the server at the start of every data stream.
type StreamHeader struct {
	Tunnel string `json:"tunnel"`
	// Remote is the visitor's address (TCP/TLS tunnels), for display.
	Remote string `json:"remote,omitempty"`
	// Route selects the local address: 0 = Bind.LocalAddr, n = Rules.Routes[n-1].
	Route int `json:"route,omitempty"`
}

// ConnEvent summarizes a finished TCP/TLS connection.
type ConnEvent struct {
	ConnectionID string  `json:"connection_id"`
	RemoteAddr   string  `json:"remote_addr"`
	BytesIn      int64   `json:"bytes_in"`  // visitor → local service
	BytesOut     int64   `json:"bytes_out"` // local service → visitor
	DurationMS   float64 `json:"duration_ms"`
	Error        string  `json:"error,omitempty"`
}

const (
	statusOK    byte = 0
	statusError byte = 1
)

func writeFrame(w io.Writer, b []byte) error {
	if len(b) > 0xffff {
		return errors.New("protocol: frame too large")
	}
	buf := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(buf, uint16(len(b)))
	copy(buf[2:], b)
	_, err := w.Write(buf)
	return err
}

func readFrame(r io.Reader) ([]byte, error) {
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return nil, err
	}
	b := make([]byte, binary.BigEndian.Uint16(l[:]))
	_, err := io.ReadFull(r, b)
	return b, err
}

// WriteStreamHeader is called by the server after opening a data stream.
func WriteStreamHeader(w io.Writer, h StreamHeader) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return writeFrame(w, b)
}

// ReadStreamHeader is called by the client on an accepted data stream.
func ReadStreamHeader(r io.Reader) (StreamHeader, error) {
	var h StreamHeader
	b, err := readFrame(r)
	if err != nil {
		return h, err
	}
	err = json.Unmarshal(b, &h)
	return h, err
}

// WriteStreamOK tells the server the local service accepted the connection.
func WriteStreamOK(w io.Writer) error {
	_, err := w.Write([]byte{statusOK})
	return err
}

// WriteStreamError tells the server the local service could not be reached.
func WriteStreamError(w io.Writer, msg string) error {
	if len(msg) > 1024 {
		msg = msg[:1024]
	}
	if _, err := w.Write([]byte{statusError}); err != nil {
		return err
	}
	return writeFrame(w, []byte(msg))
}

// LocalError is returned by ReadStreamStatus when the client could not reach
// the local service.
type LocalError struct{ Msg string }

func (e *LocalError) Error() string { return e.Msg }

// ReadStreamStatus is called by the server after writing the stream header.
func ReadStreamStatus(r io.Reader) error {
	var s [1]byte
	if _, err := io.ReadFull(r, s[:]); err != nil {
		return fmt.Errorf("tunnel closed before the local service answered: %w", err)
	}
	if s[0] == statusOK {
		return nil
	}
	b, err := readFrame(r)
	if err != nil {
		return &LocalError{Msg: "local service unavailable"}
	}
	return &LocalError{Msg: string(b)}
}

// YamuxConfig is shared by both ends so keepalives agree.
func YamuxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.KeepAliveInterval = 20 * time.Second
	c.ConnectionWriteTimeout = 30 * time.Second
	c.StreamOpenTimeout = 30 * time.Second
	c.MaxStreamWindowSize = 1024 * 1024
	c.LogOutput = io.Discard
	return c
}
