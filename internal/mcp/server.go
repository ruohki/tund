// Package tundmcp implements `tund mcp`: a Model Context Protocol server that
// lets AI agents expose local servers through tund and inspect the traffic.
// See docs/SPEC.md "Public API (v1) and MCP".
package tundmcp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"tund/internal/api"
	"tund/internal/client"
	"tund/internal/protocol"
)

// Options configure the MCP server.
type Options struct {
	Server          string // normalized server URL
	ServerSource    string // client.Source*: where Server came from
	Token           string
	ConfigPath      string // where a login is saved
	AllowPorts      []int  // local ports that may be exposed; empty = any
	RequirePassword bool   // every tunnel gets a password
	NoBrowser       bool   // never open a browser for login
	Logger          *slog.Logger
	HTTPClient      *http.Client // for API and login calls (tests)
	State           *client.State
}

// Server holds the tunnels and login state of one MCP session.
type Server struct {
	opts   Options
	log    *slog.Logger
	allow  map[int]bool
	ctx    context.Context // lives as long as the server; background work uses it
	cancel context.CancelFunc

	mu      sync.Mutex
	token   string
	tunnels map[string]*localTunnel
	order   []string
	seq     int
	login   *pendingLogin
}

func hostOfURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return raw
}

type localTunnel struct {
	terminated bool
	id         string
	password   string
	oidc       string
	allow      []string
	allowIPs   []string
	notes      []string
	t          *client.Tunnel
}

// New creates the server. Call Run to serve a transport.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{opts: opts, log: opts.Logger, ctx: ctx, cancel: cancel, token: opts.Token, tunnels: map[string]*localTunnel{}}
	if len(opts.AllowPorts) > 0 {
		s.allow = map[int]bool{}
		for _, p := range opts.AllowPorts {
			s.allow[p] = true
		}
	}
	return s
}

// Run serves one MCP session on t and stops every tunnel when the session
// ends (stdin closed) or ctx is cancelled.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	defer s.Shutdown()
	err := s.MCPServer().Run(ctx, t)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// Shutdown stops all tunnels started by this server and pending logins.
func (s *Server) Shutdown() {
	s.mu.Lock()
	var ts []*localTunnel
	for _, lt := range s.tunnels {
		ts = append(ts, lt)
	}
	s.tunnels, s.order = map[string]*localTunnel{}, nil
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, lt := range ts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lt.t.Stop()
		}()
	}
	wg.Wait()
	s.cancel()
	if len(ts) > 0 {
		s.log.Info("stopped tunnels", "count", len(ts))
	}
}

const instructions = `tund exposes local servers (e.g. a dev server you just started) on public HTTPS URLs and records every request.
Typical flow: start_tunnel {target: 3000} → share the returned url → list_requests / get_request to see what hit it → replay_request to re-send one after a fix.
The URLs are reachable by anyone on the internet: use a password for anything that isn't meant to be public.
If a tool says you are not logged in, call login and show the user the verification URL and code; they approve it in their browser.`

// MCPServer builds the SDK server with all tools registered.
func (s *Server) MCPServer() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "tund", Title: "tund tunnels", Version: client.Version}, &mcp.ServerOptions{
		Instructions: instructions,
		// The SDK logs every session event at info; keep only its problems.
		Logger: slog.New(minLevel{s.log.Handler(), slog.LevelWarn}),
	})
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "whoami",
		Title:       "Show tund account",
		Description: "Show which tund server this is connected to, whether it is logged in, the account, its static hostnames (the stable URLs tunnels get by default) and limits.",
		Annotations: readOnly,
	}, s.whoami)

	mcp.AddTool(srv, &mcp.Tool{
		Name:  "login",
		Title: "Log in to tund",
		Description: "Log this machine in to the tund server (device login). Returns a verification URL and a short code: show both to the user and ask them to open the URL, " +
			"check the code and approve. Polling happens in the background; pass wait_seconds to wait for the approval. Reports \"already_logged_in\" if a working token exists. " +
			"Does not open a browser unless open_browser is true (only set it when the user asked for that).",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: ptr(false)},
	}, s.loginTool)

	mcp.AddTool(srv, &mcp.Tool{
		Name:  "start_tunnel",
		Title: "Expose a local server",
		Description: "Expose a local HTTP(S) server on a public HTTPS URL and wait until it is online. " +
			"IMPORTANT: the URL is public on the internet; for anything sensitive (admin UIs, unreleased work, data) set a password, which visitors must enter, " +
			"or oidc to make visitors sign in with an identity provider configured in the dashboard (the app then receives X-Tund-User-Email/-Name/-Username/-Id/-Groups headers). " +
			"By default the tunnel gets the account's static URL (same every time); random=true gives a one-off URL; subdomain + pin=true claims a named static URL. " +
			"Returns the URL, an inspector link for the dashboard and warnings (e.g. nothing is listening on the port yet). The tunnel runs until stop_tunnel or the end of this session.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)},
	}, s.startTunnel)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_tunnels",
		Title:       "List tunnels",
		Description: "List the tunnels started in this session with their state and URL. all=true also lists the account's other online tunnels (e.g. from a `tund http` in a terminal).",
		Annotations: readOnly,
	}, s.listTunnels)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "stop_tunnel",
		Title:       "Stop a tunnel",
		Description: "Stop a tunnel by id or URL. Works for tunnels of this session and for any online tunnel of the account (from list_tunnels all=true).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(false)},
	}, s.stopTunnel)

	mcp.AddTool(srv, &mcp.Tool{
		Name:  "list_requests",
		Title: "List captured requests",
		Description: "List HTTP requests that went through the account's tunnels, newest first, as compact summaries (method, path, status, duration, id). " +
			"Filter by tunnel (id, URL or hostname), method, status (\"4xx\" or an exact code like \"404\") and path substring. Use get_request for headers and bodies.",
		Annotations: readOnly,
	}, s.listRequests)

	mcp.AddTool(srv, &mcp.Tool{
		Name:  "list_connections",
		Title: "List TCP/TLS connections",
		Description: "List finished connections of tcp and tls tunnels, newest first: visitor address, bytes in/out, duration and errors. " +
			"Filter by tunnel (id from start_tunnel/list_tunnels, tcp:// or tls:// URL, or public address). HTTP traffic is in list_requests instead.",
		Annotations: readOnly,
	}, s.listConnections)

	mcp.AddTool(srv, &mcp.Tool{
		Name:  "get_request",
		Title: "Show a captured request",
		Description: "Show one captured request and its response: headers and bodies (decompressed when possible). " +
			"Bodies longer than max_body_chars are cut off with a note; binary bodies are summarized, not dumped.",
		Annotations: readOnly,
	}, s.getRequest)

	mcp.AddTool(srv, &mcp.Tool{
		Name:  "replay_request",
		Title: "Replay a captured request",
		Description: "Send a captured request through its tunnel again (the tunnel must be online), optionally changing method, path, headers or body, " +
			"and return the new capture like get_request. Useful to check a fix without redoing the steps that produced the request.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(false)},
	}, s.replayRequest)
	return srv
}

func ptr[T any](v T) *T { return &v }

// minLevel drops records below a level.
type minLevel struct {
	slog.Handler
	min slog.Level
}

func (h minLevel) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}

func textResult(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}
}

// ---- auth helpers -----------------------------------------------------------

func (s *Server) currentToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

var errNotLoggedIn = errors.New("not logged in")

func (s *Server) apiClient() (*api.Client, error) {
	tok := s.currentToken()
	if tok == "" {
		return nil, fmt.Errorf("%w to %s: call the login tool and show the user the URL and code it returns", errNotLoggedIn, s.opts.Server)
	}
	c := api.New(s.opts.Server, tok)
	c.UserAgent = "tund-mcp/" + client.Version
	if s.opts.HTTPClient != nil {
		c.HTTP = s.opts.HTTPClient
	}
	return c, nil
}

// apiError makes API errors actionable for the agent.
func (s *Server) apiError(err error) error {
	switch api.StatusOf(err) {
	case http.StatusUnauthorized:
		s.mu.Lock()
		s.token = ""
		s.mu.Unlock()
		return fmt.Errorf("%s rejected the saved authtoken (it may have been revoked): call the login tool to log in again", s.opts.Server)
	case http.StatusNotFound:
		if strings.Contains(err.Error(), "HTTP 404") {
			return fmt.Errorf("%s does not provide the tund API (/_tund/api/v1); is it an up-to-date tund server?", s.opts.Server)
		}
	}
	return err
}

// ---- whoami -----------------------------------------------------------------

type WhoamiOut struct {
	Server          string               `json:"server" jsonschema:"the tund server in use"`
	LoggedIn        bool                 `json:"logged_in"`
	Account         *api.Account         `json:"account,omitempty"`
	DashboardURL    string               `json:"dashboard_url,omitempty"`
	BaseDomain      string               `json:"base_domain,omitempty"`
	ServerVersion   string               `json:"server_version,omitempty"`
	StaticHostnames []api.StaticHostname `json:"static_hostnames,omitempty"`
	Teams           []api.Team           `json:"teams,omitempty" jsonschema:"teams the account belongs to; their OIDC providers are referenced as team/provider"`
	Limits          *api.Limits          `json:"limits,omitempty" jsonschema:"per-account limits, 0 = unlimited"`
	Login           *LoginOut            `json:"login,omitempty" jsonschema:"a device login in progress"`
	Note            string               `json:"note,omitempty"`
}

func (s *Server) whoami(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, WhoamiOut, error) {
	out := WhoamiOut{Server: s.opts.Server}
	if pl := s.pendingLogin(); pl != nil {
		lo := pl.out()
		out.Login = &lo
	}
	c, err := s.apiClient()
	if err != nil {
		out.Note = "Not logged in. Call the login tool and show the user the URL and code."
		return textResult("Not logged in to %s. Call the login tool and show the user the URL and code it returns.", s.opts.Server), out, nil
	}
	me, err := c.Me(ctx)
	if err != nil {
		err = s.apiError(err)
		if s.currentToken() == "" {
			out.Note = err.Error()
			return textResult("%s", err), out, nil
		}
		return nil, out, err
	}
	out.LoggedIn = true
	out.Account = &me.Account
	out.DashboardURL, out.BaseDomain, out.ServerVersion = me.Server.DashboardURL, me.Server.BaseDomain, me.Server.Version
	out.StaticHostnames, out.Limits, out.Teams = me.StaticHostnames, &me.Limits, me.Teams

	var b strings.Builder
	fmt.Fprintf(&b, "Logged in to %s as %s.", s.opts.Server, me.Account.Email)
	for _, h := range me.StaticHostnames {
		if h.Default {
			fmt.Fprintf(&b, "\nDefault static URL (used by start_tunnel unless random/subdomain is set): %s", h.URL)
		}
	}
	if n := len(me.StaticHostnames); n > 0 {
		fmt.Fprintf(&b, "\nStatic hostnames: %d%s", n, limitSuffix(me.Limits.Pinned))
	}
	fmt.Fprintf(&b, "\nConcurrent tunnels allowed: %s", limitText(me.Limits.Tunnels))
	if len(me.Teams) > 0 {
		teams := make([]string, len(me.Teams))
		for i, t := range me.Teams {
			teams[i] = fmt.Sprintf("%s (%s)", t.Slug, t.Role)
		}
		fmt.Fprintf(&b, "\nTeams: %s", strings.Join(teams, ", "))
	}
	return textResult("%s", b.String()), out, nil
}

func limitText(n int) string {
	if n == 0 {
		return "unlimited"
	}
	return strconv.Itoa(n)
}

func limitSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return " of " + strconv.Itoa(n)
}

// ---- tunnels ----------------------------------------------------------------

type StartTunnelIn struct {
	Target        string   `json:"target" jsonschema:"the local server to expose: a port like 3000, host:port, or (http only) a URL like http://localhost:5173 or https://localhost:8443 (self-signed certificates are fine)"`
	Proto         string   `json:"proto,omitempty" jsonschema:"http (default: HTTPS URL, requests inspected), tcp (raw TCP on a public port, e.g. databases, SSH) or tls (TLS passthrough by hostname; the local service must speak TLS unless terminate_cert/terminate_key are set)"`
	RemotePort    int      `json:"remote_port,omitempty" jsonschema:"tcp only: the public port to use (must be free and in the server's range)"`
	AllowIPs      []string `json:"allow_ips,omitempty" jsonschema:"only these IPs or CIDR ranges may connect, e.g. [\"203.0.113.7\", \"10.0.0.0/8\"]; works for all protocols"`
	TerminateCert string   `json:"terminate_cert,omitempty" jsonschema:"tls only: path to a PEM certificate to terminate TLS on this machine (forward plaintext to the target)"`
	TerminateKey  string   `json:"terminate_key,omitempty" jsonschema:"tls only: path to the PEM private key for terminate_cert"`
	Subdomain     string   `json:"subdomain,omitempty" jsonschema:"request a specific subdomain of the server's domain, e.g. myapp → https://myapp.<domain>"`
	Pin           bool     `json:"pin,omitempty" jsonschema:"keep the resulting hostname as a static hostname of the account (stable URL for later runs)"`
	Random        bool     `json:"random,omitempty" jsonschema:"use a one-off random hostname instead of the account's default static one"`
	Password      string   `json:"password,omitempty" jsonschema:"require visitors to enter this password; recommended for anything not meant to be public"`
	OIDC          string   `json:"oidc,omitempty" jsonschema:"require visitors to sign in with this OIDC provider from the dashboard: <provider> or <team>/<provider>; not together with password"`
	OIDCAllow     []string `json:"oidc_allow,omitempty" jsonschema:"who may pass OIDC: emails (a@b.com), email domains (@b.com) or groups (group:admins); empty = anyone who can sign in"`
	HostHeader    string   `json:"host_header,omitempty" jsonschema:"Host header sent to the local server: preserve (default, the public hostname), rewrite (the local host:port, needed by some dev servers), or a literal value"`
	Name          string   `json:"name,omitempty" jsonschema:"label shown in the dashboard (default mcp-<port>)"`
}

type TunnelOut struct {
	ID              string   `json:"id" jsonschema:"id for stop_tunnel / list_requests / list_connections"`
	Proto           string   `json:"proto" jsonschema:"http, tcp or tls"`
	URLKind         string   `json:"url_kind" jsonschema:"what the url is: an https link for browsers, a tcp address for TCP clients, or a tls hostname (port 443)"`
	RemotePort      int      `json:"remote_port,omitempty" jsonschema:"tcp: the public port"`
	AllowIPs        []string `json:"allow_ips,omitempty"`
	TLSMode         string   `json:"tls_mode,omitempty" jsonschema:"tls: passthrough (visitors see the local service's certificate) or terminated (tund terminates TLS with the given certificate)"`
	Connections     int      `json:"connections,omitempty" jsonschema:"finished tcp/tls connections"`
	Name            string   `json:"name"`
	State           string   `json:"state" jsonschema:"connecting, online, reconnecting, failed, closed or stopped"`
	URL             string   `json:"url,omitempty"`
	Hostname        string   `json:"hostname,omitempty"`
	Local           string   `json:"local"`
	Static          bool     `json:"static"`
	Auth            string   `json:"auth" jsonschema:"none, password or oidc"`
	Password        string   `json:"password,omitempty" jsonschema:"the password visitors must enter"`
	OIDC            string   `json:"oidc,omitempty" jsonschema:"the OIDC provider visitors sign in with"`
	OIDCAllow       []string `json:"oidc_allow,omitempty"`
	IdentityHeaders []string `json:"identity_headers,omitempty" jsonschema:"request headers the local app receives about the signed-in visitor"`
	InspectorURL    string   `json:"inspector_url,omitempty" jsonschema:"dashboard page with the live request log"`
	TunnelID        string   `json:"tunnel_id,omitempty" jsonschema:"server-side id of the current connection"`
	Requests        int      `json:"requests"`
	BrowserWarning  bool     `json:"browser_warning,omitempty" jsonschema:"browsers see a one-time warning page before the site; curl/fetch/API clients are not affected"`
	Warning         string   `json:"warning,omitempty"`
	Error           string   `json:"error,omitempty"`
}

func (lt *localTunnel) out() TunnelOut {
	info := lt.t.Info()
	o := TunnelOut{
		ID: lt.id, Name: info.Name, State: string(info.State), URL: info.URL, Hostname: info.Hostname,
		Local: info.LocalAddr, Static: info.Static, Auth: orDefault(info.AuthMode, "none"), Password: lt.password,
		OIDC: lt.oidc, OIDCAllow: lt.allow,
		Proto: info.Proto, RemotePort: info.RemotePort, AllowIPs: lt.allowIPs, Connections: info.Connections,
		TunnelID: info.TunnelID, Requests: info.Requests, Error: info.Error, BrowserWarning: info.BrowserWarning,
	}
	switch o.Proto {
	case protocol.ProtoTCP:
		o.URLKind = "tcp address (not a browser link): connect with a TCP client to host " + hostOfURL(o.URL) + " port " + strconv.Itoa(o.RemotePort)
	case protocol.ProtoTLS:
		o.URLKind = "tls hostname: TLS clients connect to " + o.Hostname + ":443 (SNI routed)"
		o.TLSMode = "passthrough"
		if lt.terminated {
			o.TLSMode = "terminated"
		}
	default:
		o.Proto = protocol.ProtoHTTP
		o.URLKind = "https link: open in a browser or call with any HTTP client"
	}
	switch o.Auth {
	case "oidc":
		o.IdentityHeaders = oidcHeaders
	case "password":
		o.IdentityHeaders = []string{"X-Tund-Auth"}
	}
	if info.DashboardURL != "" && info.Hostname != "" && o.Proto == protocol.ProtoHTTP {
		o.InspectorURL = strings.TrimRight(info.DashboardURL, "/") + "/inspect?host=" + url.QueryEscape(info.Hostname)
	}
	warnings := append([]string{}, lt.notes...)
	if info.Warning != "" {
		warnings = append(warnings, info.Warning)
	}
	o.Warning = strings.Join(warnings, "; ")
	return o
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

var oidcHeaders = []string{"X-Tund-Auth", "X-Tund-User-Email", "X-Tund-User-Email-Verified", "X-Tund-User-Name",
	"X-Tund-User-Username", "X-Tund-User-Id", "X-Tund-User-Groups", "X-Tund-Idp"}

const passwordAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func randomPassword() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	var out strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			out.WriteByte('-')
		}
		out.WriteByte(passwordAlphabet[int(c)%len(passwordAlphabet)])
	}
	return out.String()
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) startTunnel(ctx context.Context, _ *mcp.CallToolRequest, in StartTunnelIn) (*mcp.CallToolResult, TunnelOut, error) {
	tok := s.currentToken()
	if tok == "" {
		return nil, TunnelOut{}, fmt.Errorf("%w to %s: call the login tool and show the user the URL and code it returns, then retry", errNotLoggedIn, s.opts.Server)
	}
	proto := strings.ToLower(strings.TrimSpace(in.Proto))
	if proto == "" {
		proto = protocol.ProtoHTTP
	}
	local, err := client.ParseTarget(proto, in.Target)
	if err != nil {
		return nil, TunnelOut{}, err
	}
	hostport := local // host:port to probe
	if proto == protocol.ProtoHTTP {
		lu, _ := url.Parse(local)
		hostport = lu.Host
	}
	lhost, lport, _ := net.SplitHostPort(hostport)
	port, _ := strconv.Atoi(lport)
	if s.allow != nil {
		if !s.allow[port] || !isLoopback(lhost) {
			return nil, TunnelOut{}, fmt.Errorf("exposing %s is not allowed: this MCP server only exposes local ports %s (--allow-ports)", local, joinPorts(s.opts.AllowPorts))
		}
	}
	in.Subdomain = strings.ToLower(strings.TrimSpace(in.Subdomain))
	if in.Random && in.Subdomain != "" {
		return nil, TunnelOut{}, errors.New("random and subdomain cannot be combined")
	}
	in.OIDC = strings.TrimSpace(in.OIDC)
	if in.Password != "" && in.OIDC != "" {
		return nil, TunnelOut{}, errors.New("use either password or oidc, not both")
	}
	lt := &localTunnel{password: in.Password, oidc: in.OIDC}
	auth, err := client.BuildAuth(in.Password, in.OIDC, in.OIDCAllow)
	if err != nil {
		return nil, TunnelOut{}, err
	}
	if auth != nil {
		lt.allow = auth.Allow
	}
	allowIPs, err := client.NormalizeAllowIPs(in.AllowIPs)
	if err != nil {
		return nil, TunnelOut{}, err
	}
	lt.allowIPs = allowIPs
	if proto != protocol.ProtoHTTP && s.opts.RequirePassword && len(allowIPs) == 0 {
		return nil, TunnelOut{}, errors.New("tund mcp runs with --require-password, but tcp/tls tunnels cannot have a password: pass allow_ips to restrict who can connect")
	}
	if auth == nil && s.opts.RequirePassword && proto == protocol.ProtoHTTP {
		lt.password = randomPassword()
		auth = &protocol.Auth{Mode: protocol.AuthPassword, Password: lt.password}
		lt.notes = append(lt.notes, "a random password was set because tund mcp runs with --require-password; share it with the people who should get access")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "mcp-" + lport
		if proto != protocol.ProtoHTTP {
			name = "mcp-" + proto + "-" + lport
		}
	}

	// Probe the target so the agent learns early that nothing is running yet.
	if conn, err := net.DialTimeout("tcp", hostport, 700*time.Millisecond); err != nil {
		what := "visitors get an error page"
		if proto != protocol.ProtoHTTP {
			what = "connections are closed right away"
		}
		lt.notes = append(lt.notes, fmt.Sprintf("nothing is listening on %s yet; %s until the local server is started", hostport, what))
	} else {
		conn.Close()
	}

	spec := client.TunnelSpec{Name: name, LocalAddr: local, Subdomain: in.Subdomain, HostHeader: in.HostHeader, Random: in.Random, Pin: in.Pin, Auth: auth,
		Proto: proto, RemotePort: in.RemotePort, AllowIPs: allowIPs, TerminateCert: in.TerminateCert, TerminateKey: in.TerminateKey}
	if err := spec.Validate(); err != nil {
		return nil, TunnelOut{}, err
	}
	lt.terminated = in.TerminateCert != ""
	s.mu.Lock()
	s.seq++
	lt.id = fmt.Sprintf("tun-%d", s.seq)
	s.mu.Unlock()
	log := s.log.With("tunnel", lt.id, "local", local)
	t, err := client.StartTunnel(client.TunnelOptions{
		Server: s.opts.Server, Authtoken: tok, Spec: spec, State: s.opts.State,
		OnEvent: func(e client.Event) {
			switch e.Kind {
			case client.EventBound:
				log.Info("tunnel online", "url", e.URL, "static", e.Static, "warning", e.Warning)
			case client.EventFailed, client.EventClosed:
				log.Warn("tunnel "+string(e.Kind), "error", e.Error)
			case client.EventReconnecting:
				log.Warn("reconnecting", "error", e.Error, "in", e.Delay)
			case client.EventRequest:
				log.Debug("request", "method", e.Request.Method, "path", e.Request.Path, "status", e.Request.Status)
			}
		},
	})
	if err != nil {
		return nil, TunnelOut{}, err
	}
	lt.t = t
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	info, err := t.WaitReady(wctx)
	if err != nil {
		t.Stop()
		var ae *client.AuthError
		if errors.As(t.Err(), &ae) {
			s.mu.Lock()
			s.token = ""
			s.mu.Unlock()
			return nil, TunnelOut{}, fmt.Errorf("%v: call the login tool to log in again", ae)
		}
		return nil, TunnelOut{}, fmt.Errorf("could not start the tunnel for %s: %v", local, err)
	}
	s.mu.Lock()
	s.tunnels[lt.id] = lt
	s.order = append(s.order, lt.id)
	s.mu.Unlock()

	out := lt.out()
	var b strings.Builder
	fmt.Fprintf(&b, "Tunnel %s is online: %s → %s", lt.id, info.URL, local)
	if out.Static {
		b.WriteString(" (static address, stays the same next time)")
	}
	switch proto {
	case protocol.ProtoTCP:
		host := hostOfURL(out.URL)
		fmt.Fprintf(&b, "\nThis is a raw TCP address, not a browser link: connect with a TCP client to host %s, port %d (e.g. `nc %s %d`, `psql -h %s -p %d`).",
			host, out.RemotePort, host, out.RemotePort, host, out.RemotePort)
	case protocol.ProtoTLS:
		if lt.terminated {
			fmt.Fprintf(&b, "\nTLS clients connect to %s:443; tund terminates TLS on this machine with the given certificate and forwards plaintext to %s.", out.Hostname, local)
		} else {
			fmt.Fprintf(&b, "\nTLS passthrough: clients connect to %s:443 and talk TLS directly to %s, so they see its certificate (it must be valid for %s to avoid warnings).", out.Hostname, local, out.Hostname)
		}
	}
	switch {
	case len(lt.allowIPs) > 0 && lt.oidc == "" && lt.password == "":
		fmt.Fprintf(&b, "\nOnly these addresses may connect: %s.", strings.Join(lt.allowIPs, ", "))
	case proto != protocol.ProtoHTTP:
		b.WriteString("\nThe address is public: anyone who knows it can connect. Use allow_ips to restrict it.")
	case lt.oidc != "":
		who := "anyone who can sign in"
		if len(lt.allow) > 0 {
			who = strings.Join(lt.allow, ", ")
		}
		fmt.Fprintf(&b, "\nVisitors must sign in with the OIDC provider %s (allowed: %s).", lt.oidc, who)
		b.WriteString("\nThe local app receives the visitor's identity in request headers: X-Tund-Auth: oidc, X-Tund-User-Email, X-Tund-User-Name, " +
			"X-Tund-User-Username, X-Tund-User-Id, X-Tund-User-Groups (comma-separated), X-Tund-Idp (only the claims the provider sends). " +
			"tund strips X-Tund-* headers sent by visitors, so the app can trust them.")
	case lt.password != "":
		fmt.Fprintf(&b, "\nVisitors must enter the password: %s (the app receives X-Tund-Auth: password)", lt.password)
	case len(lt.allowIPs) > 0:
		fmt.Fprintf(&b, "\nOnly these addresses may connect: %s.", strings.Join(lt.allowIPs, ", "))
	default:
		b.WriteString("\nThe URL is public: anyone with the link can reach the local server.")
	}
	if len(lt.allowIPs) > 0 && (lt.oidc != "" || lt.password != "") {
		fmt.Fprintf(&b, "\nAdditionally only these addresses may connect: %s.", strings.Join(lt.allowIPs, ", "))
	}
	if out.InspectorURL != "" {
		fmt.Fprintf(&b, "\nInspector: %s", out.InspectorURL)
	}
	if out.BrowserWarning {
		fmt.Fprintf(&b, "\nNote: people opening the URL in a browser first see a one-time tund warning page they must click through; curl, fetch and other API clients are not affected. "+
			"To skip it (e.g. in automated browser tests), send the request header %s: 1, or protect the tunnel with a password.", protocol.HeaderSkipWarning)
	}
	if out.Warning != "" {
		fmt.Fprintf(&b, "\nWarning: %s", out.Warning)
	}
	return textResult("%s", b.String()), out, nil
}

func joinPorts(ps []int) string {
	s := make([]string, len(ps))
	for i, p := range ps {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ", ")
}

type ListTunnelsIn struct {
	All bool `json:"all,omitempty" jsonschema:"also list the account's other online tunnels, e.g. from tund http in a terminal"`
}

type ListTunnelsOut struct {
	Tunnels []TunnelOut  `json:"tunnels" jsonschema:"tunnels started by this MCP session"`
	Other   []api.Tunnel `json:"other,omitempty" jsonschema:"other online tunnels of the account (all=true)"`
}

func (s *Server) localTunnels() []*localTunnel {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*localTunnel, 0, len(s.order))
	for _, id := range s.order {
		if lt, ok := s.tunnels[id]; ok {
			out = append(out, lt)
		}
	}
	return out
}

func (s *Server) listTunnels(ctx context.Context, _ *mcp.CallToolRequest, in ListTunnelsIn) (*mcp.CallToolResult, ListTunnelsOut, error) {
	out := ListTunnelsOut{Tunnels: []TunnelOut{}}
	mine := map[string]bool{}
	var b strings.Builder
	for _, lt := range s.localTunnels() {
		o := lt.out()
		out.Tunnels = append(out.Tunnels, o)
		if o.TunnelID != "" {
			mine[o.TunnelID] = true
		}
		fmt.Fprintf(&b, "%s  %-12s %s → %s  (%d requests)", o.ID, o.State, orDefault(o.URL, "-"), o.Local, o.Requests)
		if o.Auth != "none" {
			fmt.Fprintf(&b, " [%s]", o.Auth)
		}
		if o.Error != "" {
			fmt.Fprintf(&b, "  error: %s", o.Error)
		}
		b.WriteByte('\n')
	}
	if len(out.Tunnels) == 0 {
		b.WriteString("No tunnels started in this session.\n")
	}
	if in.All {
		c, err := s.apiClient()
		if err != nil {
			return nil, out, err
		}
		all, err := c.Tunnels(ctx)
		if err != nil {
			return nil, out, s.apiError(err)
		}
		for _, t := range all {
			if mine[t.ID] {
				continue
			}
			out.Other = append(out.Other, t)
			fmt.Fprintf(&b, "%s  %s → %s  (%s on %s)\n", t.ID, t.URL, t.LocalAddr, orDefault(t.Name, "unnamed"), orDefault(t.Client.Hostname, "unknown host"))
		}
		if len(out.Other) == 0 {
			b.WriteString("No other online tunnels on the account.\n")
		}
	}
	return textResult("%s", strings.TrimRight(b.String(), "\n")), out, nil
}

type StopTunnelIn struct {
	ID  string `json:"id,omitempty" jsonschema:"tunnel id from start_tunnel or list_tunnels"`
	URL string `json:"url,omitempty" jsonschema:"public URL or hostname of the tunnel"`
}

type StopTunnelOut struct {
	Stopped bool   `json:"stopped"`
	ID      string `json:"id"`
	URL     string `json:"url,omitempty"`
}

func hostFromURLish(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil {
			return u.Hostname()
		}
	}
	return strings.TrimRight(s, "/")
}

func (s *Server) stopTunnel(ctx context.Context, _ *mcp.CallToolRequest, in StopTunnelIn) (*mcp.CallToolResult, StopTunnelOut, error) {
	if in.ID == "" && in.URL == "" {
		return nil, StopTunnelOut{}, errors.New("pass the tunnel's id or url")
	}
	host := hostFromURLish(in.URL)
	s.mu.Lock()
	var found *localTunnel
	for _, lt := range s.tunnels {
		info := lt.t.Info()
		if (in.ID != "" && (lt.id == in.ID || info.TunnelID == in.ID)) || (host != "" && info.Hostname == host) {
			found = lt
			break
		}
	}
	if found != nil {
		delete(s.tunnels, found.id)
	}
	s.mu.Unlock()
	if found != nil {
		info := found.t.Info()
		found.t.Stop()
		return textResult("Stopped %s (%s).", found.id, orDefault(info.URL, info.LocalAddr)), StopTunnelOut{Stopped: true, ID: found.id, URL: info.URL}, nil
	}

	c, err := s.apiClient()
	if err != nil {
		return nil, StopTunnelOut{}, err
	}
	id, u := in.ID, ""
	if id == "" || host != "" {
		all, err := c.Tunnels(ctx)
		if err != nil {
			return nil, StopTunnelOut{}, s.apiError(err)
		}
		id = ""
		for _, t := range all {
			if (in.ID != "" && t.ID == in.ID) || (host != "" && t.Hostname == host) {
				id, u = t.ID, t.URL
				break
			}
		}
		if id == "" {
			return nil, StopTunnelOut{}, fmt.Errorf("no online tunnel matches %q", orDefault(in.URL, in.ID))
		}
	}
	if err := c.StopTunnel(ctx, id); err != nil {
		if api.StatusOf(err) == http.StatusNotFound {
			return nil, StopTunnelOut{}, fmt.Errorf("no online tunnel with id %q (list_tunnels all=true shows the current ones)", id)
		}
		return nil, StopTunnelOut{}, s.apiError(err)
	}
	return textResult("Stopped tunnel %s %s.", id, u), StopTunnelOut{Stopped: true, ID: id, URL: u}, nil
}

// ---- requests ---------------------------------------------------------------

type ListRequestsIn struct {
	Tunnel string `json:"tunnel,omitempty" jsonschema:"only requests of this tunnel: id from start_tunnel/list_tunnels, public URL or hostname"`
	Method string `json:"method,omitempty" jsonschema:"HTTP method, e.g. POST"`
	Status string `json:"status,omitempty" jsonschema:"status class 2xx/3xx/4xx/5xx or an exact code such as 404"`
	Path   string `json:"path,omitempty" jsonschema:"substring of the path (including the query string)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum number of requests (1-200, default 20)"`
	Before string `json:"before,omitempty" jsonschema:"cursor for older requests: next_before from a previous call"`
}

type RequestLine struct {
	ID         string  `json:"id"`
	StartedAt  string  `json:"started_at"`
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	Status     int     `json:"status" jsonschema:"0 = no response, see error"`
	DurationMS float64 `json:"duration_ms"`
	Hostname   string  `json:"hostname"`
	Error      string  `json:"error,omitempty"`
	ReplayOf   string  `json:"replay_of,omitempty"`
}

type ListRequestsOut struct {
	Requests   []RequestLine `json:"requests"`
	NextBefore string        `json:"next_before,omitempty"`
}

func (s *Server) resolveTunnelFilter(f *api.RequestFilter, tunnel string) {
	tunnel = strings.TrimSpace(tunnel)
	if tunnel == "" {
		return
	}
	s.mu.Lock()
	lt := s.tunnels[tunnel]
	s.mu.Unlock()
	switch {
	case lt != nil:
		f.Hostname = lt.t.Info().Hostname
		if f.Hostname == "" {
			f.TunnelID = "-" // not bound yet: match nothing
		}
	case strings.Contains(tunnel, ".") || strings.Contains(tunnel, "://"):
		f.Hostname = hostFromURLish(tunnel)
	default:
		f.TunnelID = tunnel
	}
}

func summaryLine(r api.RequestSummary) RequestLine {
	return RequestLine{ID: r.ID, StartedAt: r.StartedAt, Method: r.Method, Path: r.Path, Status: r.Status,
		DurationMS: r.DurationMS, Hostname: r.Hostname, Error: r.Error, ReplayOf: r.ReplayOf}
}

func shortTime(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.Local().Format("15:04:05")
}

func statusText(status int, errMsg string) string {
	if status == 0 {
		return "ERR " + errMsg
	}
	return strconv.Itoa(status)
}

func (s *Server) listRequests(ctx context.Context, _ *mcp.CallToolRequest, in ListRequestsIn) (*mcp.CallToolResult, ListRequestsOut, error) {
	c, err := s.apiClient()
	if err != nil {
		return nil, ListRequestsOut{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 200)
	f := api.RequestFilter{Method: strings.ToUpper(strings.TrimSpace(in.Method)), Path: in.Path, Limit: limit, Before: in.Before}
	exact := 0
	if st := strings.ToLower(strings.TrimSpace(in.Status)); st != "" {
		switch {
		case len(st) == 3 && st[1:] == "xx" && st[0] >= '1' && st[0] <= '5':
			f.Status = st
		default:
			n, err := strconv.Atoi(st)
			if err != nil || n < 100 || n > 599 {
				return nil, ListRequestsOut{}, fmt.Errorf("status must be a class like 4xx or a code like 404, not %q", in.Status)
			}
			exact, f.Status = n, fmt.Sprintf("%dxx", n/100)
		}
	}
	s.resolveTunnelFilter(&f, in.Tunnel)
	list, err := c.Requests(ctx, f)
	if err != nil {
		return nil, ListRequestsOut{}, s.apiError(err)
	}
	out := ListRequestsOut{Requests: []RequestLine{}, NextBefore: list.NextBefore}
	var b strings.Builder
	for _, r := range list.Requests {
		if exact != 0 && r.Status != exact {
			continue
		}
		out.Requests = append(out.Requests, summaryLine(r))
		fmt.Fprintf(&b, "%s  %-6s %s  → %s  %.0fms  %s  id=%s\n", shortTime(r.StartedAt), r.Method, r.Path, statusText(r.Status, r.Error), r.DurationMS, r.Hostname, r.ID)
	}
	if len(out.Requests) == 0 {
		b.WriteString("No matching requests.")
	} else if list.NextBefore != "" && len(list.Requests) == limit {
		fmt.Fprintf(&b, "(more: pass before=%q)", list.NextBefore)
	}
	return textResult("%s", strings.TrimRight(b.String(), "\n")), out, nil
}

type GetRequestIn struct {
	ID           string `json:"id" jsonschema:"request id from list_requests"`
	MaxBodyChars int    `json:"max_body_chars,omitempty" jsonschema:"cut each body after this many characters (default 20000)"`
}

type BodyOut struct {
	Size            int64  `json:"size" jsonschema:"full size on the wire in bytes"`
	ContentType     string `json:"content_type,omitempty"`
	ContentEncoding string `json:"content_encoding,omitempty"`
	Text            string `json:"text,omitempty" jsonschema:"the (decompressed) body when it is text"`
	Note            string `json:"note,omitempty" jsonschema:"why the body is incomplete or not shown"`
}

type MessageOut struct {
	Headers map[string][]string `json:"headers,omitempty"`
	Body    BodyOut             `json:"body"`
}

type RequestDetail struct {
	RequestLine
	SignedIn string     `json:"signed_in,omitempty" jsonschema:"who the visitor was, from the X-Tund-* identity headers tund added"`
	Proto    string     `json:"proto,omitempty"`
	TTFBMS   float64    `json:"ttfb_ms"`
	Remote   string     `json:"remote_addr,omitempty"`
	Request  MessageOut `json:"request"`
	Response MessageOut `json:"response"`
}

func bodyOut(b api.Body, max int) BodyOut {
	o := BodyOut{Size: b.Size, ContentType: b.ContentType, ContentEncoding: b.ContentEncoding}
	var notes []string
	switch {
	case b.Text != "":
		o.Text = b.Text
		if r := []rune(o.Text); len(r) > max {
			o.Text = string(r[:max])
			notes = append(notes, fmt.Sprintf("showing the first %d of %d characters (raise max_body_chars to see more)", max, len(r)))
		}
	case b.Base64 != "":
		notes = append(notes, fmt.Sprintf("binary body (%d bytes captured, %s), not shown", len(b.Base64)*3/4, orDefault(b.ContentType, "unknown type")))
	case b.Size > 0:
		notes = append(notes, "body was not captured")
	}
	if b.Truncated {
		notes = append(notes, "the server only captured the beginning of this body")
	}
	if b.ContentEncoding != "" && !b.Decoded && b.Text == "" {
		notes = append(notes, "could not decode Content-Encoding "+b.ContentEncoding)
	}
	o.Note = strings.Join(notes, "; ")
	return o
}

func detail(r *api.Request, max int) RequestDetail {
	if max <= 0 {
		max = 20000
	}
	return RequestDetail{
		RequestLine: summaryLine(r.RequestSummary),
		SignedIn:    signedIn(r.Request.Headers),
		Proto:       r.Proto, TTFBMS: r.TTFBMS, Remote: r.RemoteAddr,
		Request:  MessageOut{Headers: r.Request.Headers, Body: bodyOut(r.Request.Body, max)},
		Response: MessageOut{Headers: r.Response.Headers, Body: bodyOut(r.Response.Body, max)},
	}
}

func header(h map[string][]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// signedIn summarizes the identity headers the edge added for a visitor.
func signedIn(h map[string][]string) string {
	mode := header(h, "X-Tund-Auth")
	email := header(h, "X-Tund-User-Email")
	if mode == "" && email == "" {
		return ""
	}
	if mode == "password" {
		return "Visitor passed the password check (X-Tund-Auth: password)"
	}
	who := email
	for _, alt := range []string{"X-Tund-User-Username", "X-Tund-User-Id"} {
		if who == "" {
			who = header(h, alt)
		}
	}
	var extra []string
	if name := header(h, "X-Tund-User-Name"); name != "" {
		extra = append(extra, name)
	}
	if groups := header(h, "X-Tund-User-Groups"); groups != "" {
		extra = append(extra, "groups: "+groups)
	}
	if idp := header(h, "X-Tund-Idp"); idp != "" {
		extra = append(extra, "via "+idp)
	}
	s := "Signed in as " + orDefault(who, "an unknown user")
	if len(extra) > 0 {
		s += " (" + strings.Join(extra, ", ") + ")"
	}
	return s
}

func writeHeaders(b *strings.Builder, prefix string, h map[string][]string) {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(b, "%s%s: %s\n", prefix, k, v)
		}
	}
}

func writeBody(b *strings.Builder, body BodyOut) {
	if body.Text != "" {
		b.WriteString("\n" + body.Text + "\n")
	}
	if body.Note != "" {
		fmt.Fprintf(b, "[%s]\n", body.Note)
	}
}

// render prints a request like an HTTP exchange.
func render(d RequestDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s  →  %s  (%.0f ms)  host=%s  id=%s", d.Method, d.Path, statusText(d.Status, d.Error), d.DurationMS, d.Hostname, d.ID)
	if d.ReplayOf != "" {
		fmt.Fprintf(&b, "  replay of %s", d.ReplayOf)
	}
	b.WriteString("\n\n")
	if d.SignedIn != "" {
		b.WriteString(d.SignedIn + "\n")
	}
	fmt.Fprintf(&b, "> %s %s %s\n", d.Method, d.Path, d.Proto)
	writeHeaders(&b, "> ", d.Request.Headers)
	writeBody(&b, d.Request.Body)
	if d.Status != 0 {
		fmt.Fprintf(&b, "\n< %d %s\n", d.Status, http.StatusText(d.Status))
		writeHeaders(&b, "< ", d.Response.Headers)
		writeBody(&b, d.Response.Body)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *Server) getRequest(ctx context.Context, _ *mcp.CallToolRequest, in GetRequestIn) (*mcp.CallToolResult, RequestDetail, error) {
	c, err := s.apiClient()
	if err != nil {
		return nil, RequestDetail{}, err
	}
	r, err := c.Request(ctx, strings.TrimSpace(in.ID))
	if err != nil {
		if api.StatusOf(err) == http.StatusNotFound {
			return nil, RequestDetail{}, fmt.Errorf("no captured request with id %q (it may be older than the retention period)", in.ID)
		}
		return nil, RequestDetail{}, s.apiError(err)
	}
	d := detail(r, in.MaxBodyChars)
	return textResult("%s", render(d)), d, nil
}

type ReplayRequestIn struct {
	ID           string            `json:"id" jsonschema:"request id from list_requests"`
	Method       string            `json:"method,omitempty" jsonschema:"override the method"`
	Path         string            `json:"path,omitempty" jsonschema:"override the path, including the query string"`
	Headers      map[string]string `json:"headers,omitempty" jsonschema:"headers to set or replace"`
	Body         *string           `json:"body,omitempty" jsonschema:"replace the body with this text"`
	MaxBodyChars int               `json:"max_body_chars,omitempty" jsonschema:"cut each body of the result after this many characters (default 20000)"`
}

type ReplayOut struct {
	RequestID string         `json:"request_id" jsonschema:"id of the new capture"`
	Status    int            `json:"status"`
	Request   *RequestDetail `json:"request,omitempty" jsonschema:"the new capture, like get_request"`
	Note      string         `json:"note,omitempty"`
}

// replayWait is how long replay_request waits for the new capture.
var replayWait = 3 * time.Second

func (s *Server) replayRequest(ctx context.Context, _ *mcp.CallToolRequest, in ReplayRequestIn) (*mcp.CallToolResult, ReplayOut, error) {
	c, err := s.apiClient()
	if err != nil {
		return nil, ReplayOut{}, err
	}
	var opts *api.ReplayOptions
	if in.Method != "" || in.Path != "" || len(in.Headers) > 0 || in.Body != nil {
		opts = &api.ReplayOptions{Method: strings.ToUpper(in.Method), Path: in.Path, Body: in.Body}
		if len(in.Headers) > 0 {
			opts.Headers = map[string][]string{}
			for k, v := range in.Headers {
				opts.Headers[k] = []string{v}
			}
		}
	}
	res, err := c.Replay(ctx, strings.TrimSpace(in.ID), opts)
	if err != nil {
		switch api.StatusOf(err) {
		case http.StatusNotFound:
			return nil, ReplayOut{}, fmt.Errorf("no captured request with id %q", in.ID)
		case http.StatusConflict:
			return nil, ReplayOut{}, fmt.Errorf("the tunnel for this request is offline; start it again (start_tunnel) and retry: %v", err)
		}
		return nil, ReplayOut{}, s.apiError(err)
	}
	out := ReplayOut{RequestID: res.RequestID, Status: res.Status}
	deadline := time.Now().Add(replayWait)
	for {
		r, err := c.Request(ctx, res.RequestID)
		if err == nil {
			d := detail(r, in.MaxBodyChars)
			out.Request = &d
			return textResult("Replayed %s as %s.\n\n%s", in.ID, res.RequestID, render(d)), out, nil
		}
		if api.StatusOf(err) != http.StatusNotFound {
			return nil, out, s.apiError(err)
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, out, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	out.Note = "the capture is not stored yet; call get_request with request_id in a moment"
	return textResult("Replayed %s: status %d, new request id %s (%s).", in.ID, res.Status, res.RequestID, out.Note), out, nil
}

// ---- connections ------------------------------------------------------------

type ListConnectionsIn struct {
	Tunnel string `json:"tunnel,omitempty" jsonschema:"only connections of this tunnel: id from start_tunnel/list_tunnels, tcp:// or tls:// URL, or public address (host:port for tcp, hostname for tls)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum number of connections (1-200, default 20)"`
	Before string `json:"before,omitempty" jsonschema:"cursor for older connections: next_before from a previous call"`
}

type ListConnectionsOut struct {
	Connections []api.Connection `json:"connections"`
	NextBefore  string           `json:"next_before,omitempty"`
}

// addressOf turns a tcp:// or tls:// URL into the API's address form.
func addressOf(u string) string {
	u = strings.TrimSpace(u)
	for _, p := range []string{"tcp://", "tls://", "https://", "http://"} {
		if rest, ok := strings.CutPrefix(strings.ToLower(u), p); ok {
			return strings.TrimRight(rest, "/")
		}
	}
	return strings.ToLower(strings.TrimRight(u, "/"))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (s *Server) listConnections(ctx context.Context, _ *mcp.CallToolRequest, in ListConnectionsIn) (*mcp.CallToolResult, ListConnectionsOut, error) {
	c, err := s.apiClient()
	if err != nil {
		return nil, ListConnectionsOut{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	f := api.ConnectionFilter{Limit: min(limit, 200), Before: in.Before}
	if t := strings.TrimSpace(in.Tunnel); t != "" {
		s.mu.Lock()
		lt := s.tunnels[t]
		s.mu.Unlock()
		switch {
		case lt != nil:
			if f.Address = addressOf(lt.t.Info().URL); f.Address == "" {
				f.TunnelID = "-" // not bound yet: match nothing
			}
		case strings.ContainsAny(t, ".:"):
			f.Address = addressOf(t)
		default:
			f.TunnelID = t
		}
	}
	list, err := c.ListConnections(ctx, f)
	if err != nil {
		return nil, ListConnectionsOut{}, s.apiError(err)
	}
	out := ListConnectionsOut{Connections: list.Connections, NextBefore: list.NextBefore}
	if out.Connections == nil {
		out.Connections = []api.Connection{}
	}
	var b strings.Builder
	for _, cn := range out.Connections {
		fmt.Fprintf(&b, "%s  %s → %s  ↑%s ↓%s  %.2fs  id=%s", shortTime(cn.StartedAt), cn.RemoteAddr, cn.Address,
			humanBytes(cn.BytesIn), humanBytes(cn.BytesOut), cn.DurationMS/1000, cn.ID)
		if cn.Error != "" {
			fmt.Fprintf(&b, "  error: %s", cn.Error)
		}
		b.WriteByte('\n')
	}
	if len(out.Connections) == 0 {
		b.WriteString("No matching connections.")
	} else if list.NextBefore != "" && len(list.Connections) == f.Limit {
		fmt.Fprintf(&b, "(more: pass before=%q)", list.NextBefore)
	}
	return textResult("%s", strings.TrimRight(b.String(), "\n")), out, nil
}
