// Package api is a small client for the tund public HTTP API
// (/_tund/api/v1, see docs/SPEC.md "Public API (v1) and MCP").
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"tund/internal/tlsutil"
)

// BasePath is the API prefix on the dashboard host.
const BasePath = "/_tund/api/v1"

// Client talks to one tund server with one authtoken.
type Client struct {
	server    string // e.g. https://tund.io (no trailing slash)
	token     string
	HTTP      *http.Client
	UserAgent string
}

// New creates a client. server is the normalized base URL of the dashboard host.
func New(server, token string) *Client {
	return &Client{
		server:    strings.TrimRight(server, "/"),
		token:     token,
		HTTP:      &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tlsutil.MustClientConfig()}},
		UserAgent: "tund-api",
	}
}

// Server returns the base URL the client talks to.
func (c *Client) Server() string { return c.server }

// Error is a non-2xx API response.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("tund API: HTTP %d %s", e.Status, http.StatusText(e.Status))
	}
	return "tund API: " + e.Message
}

// StatusOf returns the HTTP status of an *Error, or 0.
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

type Account struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"is_admin"`
}

type ServerInfo struct {
	BaseDomain   string `json:"base_domain"`
	DashboardURL string `json:"dashboard_url"`
	Version      string `json:"version"`
}

// Limits are per-account limits; 0 means unlimited.
type Limits struct {
	Tunnels int `json:"tunnels"`
	Pinned  int `json:"pinned"`
	Domains int `json:"domains"`
}

type StaticHostname struct {
	Hostname string `json:"hostname"`
	URL      string `json:"url"`
	Default  bool   `json:"default"`
}

// Team is a team the account belongs to.
type Team struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"` // owner | admin | member
}

type Me struct {
	Account         Account          `json:"account"`
	Server          ServerInfo       `json:"server"`
	Limits          Limits           `json:"limits"`
	StaticHostnames []StaticHostname `json:"static_hostnames"`
	Teams           []Team           `json:"teams"`
}

type TunnelClient struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Version  string `json:"version"`
}

type Tunnel struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Hostname   string       `json:"hostname"`
	URL        string       `json:"url"`
	LocalAddr  string       `json:"local_addr"`
	AuthMode   string       `json:"auth_mode"`
	Static     bool         `json:"static"`
	StartedAt  string       `json:"started_at"`
	Client     TunnelClient `json:"client"`
	Proto      string       `json:"proto"`       // http, tcp or tls
	RemotePort int          `json:"remote_port"` // tcp
}

// RequestSummary is one captured exchange without headers and bodies.
type RequestSummary struct {
	ID           string  `json:"id"`
	TunnelID     string  `json:"tunnel_id"`
	Hostname     string  `json:"hostname"`
	Method       string  `json:"method"`
	Path         string  `json:"path"`
	Status       int     `json:"status"` // 0 = no response, see Error
	DurationMS   float64 `json:"duration_ms"`
	TTFBMS       float64 `json:"ttfb_ms"`
	ReqBodySize  int64   `json:"req_body_size"`
	RespBodySize int64   `json:"resp_body_size"`
	RemoteAddr   string  `json:"remote_addr"`
	Error        string  `json:"error"`
	ReplayOf     string  `json:"replay_of"`
	StartedAt    string  `json:"started_at"`
}

// Body is a captured body. Text is set for (decoded) UTF-8 text, Base64 otherwise.
type Body struct {
	Size            int64  `json:"size"`
	Truncated       bool   `json:"truncated"`
	ContentType     string `json:"content_type"`
	ContentEncoding string `json:"content_encoding"`
	Decoded         bool   `json:"decoded"`
	Text            string `json:"text"`
	Base64          string `json:"base64"`
}

type Exchange struct {
	Headers map[string][]string `json:"headers"`
	Body    Body                `json:"body"`
}

type Request struct {
	RequestSummary
	Proto    string   `json:"proto"`
	Request  Exchange `json:"request"`
	Response Exchange `json:"response"`
}

// RequestFilter selects captured requests. Zero values are omitted.
type RequestFilter struct {
	Hostname string
	TunnelID string
	Method   string
	Status   string // "2xx" … "5xx"
	Path     string // substring
	Limit    int    // 1..200, server default 50
	Before   string // RFC3339Nano cursor from RequestList.NextBefore
}

type RequestList struct {
	Requests   []RequestSummary `json:"requests"`
	NextBefore string           `json:"next_before"`
}

// ReplayOptions override parts of the original request. Nil replays as-is.
type ReplayOptions struct {
	Method     string              `json:"method,omitempty"`
	Path       string              `json:"path,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       *string             `json:"body,omitempty"`
	BodyBase64 string              `json:"body_base64,omitempty"`
}

type ReplayResult struct {
	RequestID string `json:"request_id"`
	Status    int    `json:"status"`
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	u := c.server + BasePath + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		e := &Error{Status: resp.StatusCode}
		var m struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &m) == nil {
			e.Message = m.Error
		}
		return e
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("tund API: invalid response from %s: %w", path, err)
	}
	return nil
}

// Me returns the account, server info, limits and static hostnames.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var me Me
	return &me, c.do(ctx, http.MethodGet, "/me", nil, nil, &me)
}

// Tunnels lists the account's online tunnels.
func (c *Client) Tunnels(ctx context.Context) ([]Tunnel, error) {
	var r struct {
		Tunnels []Tunnel `json:"tunnels"`
	}
	err := c.do(ctx, http.MethodGet, "/tunnels", nil, nil, &r)
	return r.Tunnels, err
}

// StopTunnel stops an online tunnel of the account.
func (c *Client) StopTunnel(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/tunnels/"+url.PathEscape(id)+"/stop", nil, struct{}{}, nil)
}

// Requests lists captured requests, newest first.
func (c *Client) Requests(ctx context.Context, f RequestFilter) (*RequestList, error) {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("hostname", f.Hostname)
	set("tunnel_id", f.TunnelID)
	set("method", f.Method)
	set("status", f.Status)
	set("path", f.Path)
	set("before", f.Before)
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	var r RequestList
	return &r, c.do(ctx, http.MethodGet, "/requests", q, nil, &r)
}

// Request returns one captured request with headers and bodies.
func (c *Client) Request(ctx context.Context, id string) (*Request, error) {
	var r Request
	return &r, c.do(ctx, http.MethodGet, "/requests/"+url.PathEscape(id), nil, nil, &r)
}

// Connection is a finished TCP/TLS connection.
type Connection struct {
	ID         string  `json:"id"`
	TunnelID   string  `json:"tunnel_id"`
	Proto      string  `json:"proto"`
	Address    string  `json:"address"` // public address: host:port (tcp) or hostname (tls)
	RemoteAddr string  `json:"remote_addr"`
	BytesIn    int64   `json:"bytes_in"`  // visitor → local service
	BytesOut   int64   `json:"bytes_out"` // local service → visitor
	DurationMS float64 `json:"duration_ms"`
	Error      string  `json:"error"`
	StartedAt  string  `json:"started_at"`
}

// ConnectionFilter selects connections. Zero values are omitted.
type ConnectionFilter struct {
	TunnelID string
	Address  string
	Limit    int
	Before   string
}

type ConnectionList struct {
	Connections []Connection `json:"connections"`
	NextBefore  string       `json:"next_before"`
}

// ListConnections lists finished TCP/TLS connections, newest first.
func (c *Client) ListConnections(ctx context.Context, f ConnectionFilter) (*ConnectionList, error) {
	q := url.Values{}
	if f.TunnelID != "" {
		q.Set("tunnel_id", f.TunnelID)
	}
	if f.Address != "" {
		q.Set("address", f.Address)
	}
	if f.Before != "" {
		q.Set("before", f.Before)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	var r ConnectionList
	return &r, c.do(ctx, http.MethodGet, "/connections", q, nil, &r)
}

// Replay sends a captured request through its tunnel again.
func (c *Client) Replay(ctx context.Context, id string, opts *ReplayOptions) (*ReplayResult, error) {
	var in any = struct{}{}
	if opts != nil {
		in = opts
	}
	var r ReplayResult
	return &r, c.do(ctx, http.MethodPost, "/requests/"+url.PathEscape(id)+"/replay", nil, in, &r)
}
