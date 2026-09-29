package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"tund/internal/protocol"
)

// ParseLocalAddr normalizes a local address given on the command line into a
// URL with an explicit port: "3000" → "http://localhost:3000",
// "host:8080" → "http://host:8080", "https://localhost" → "https://localhost:443".
func ParseLocalAddr(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("missing local address")
	}
	if isDigits(s) {
		if err := checkPort(s); err != nil {
			return "", err
		}
		return "http://localhost:" + s, nil
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("invalid local address %q: %v", s, err)
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme != "http" && scheme != "https" {
			return "", fmt.Errorf("unsupported scheme %q in %q: only http and https are supported", u.Scheme, s)
		}
		if u.User != nil {
			return "", fmt.Errorf("local address %q must not contain credentials", s)
		}
		if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return "", fmt.Errorf("local address %q must not contain a path", s)
		}
		host, port := u.Hostname(), u.Port()
		if host == "" {
			return "", fmt.Errorf("local address %q has no host", s)
		}
		if port == "" {
			port = "80"
			if scheme == "https" {
				port = "443"
			}
		} else if err := checkPort(port); err != nil {
			return "", err
		}
		return scheme + "://" + net.JoinHostPort(host, port), nil
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", fmt.Errorf("invalid local address %q: expected a port, host:port or http(s):// URL", s)
	}
	if host == "" {
		host = "localhost"
	}
	if err := checkPort(port); err != nil {
		return "", err
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func checkPort(p string) error {
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid port %q", p)
	}
	return nil
}

// NormalizeServer turns user input such as "dashboard.example.com" into a
// canonical base URL like "https://dashboard.example.com".
func NormalizeServer(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("no server configured")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid server URL %q: %v", s, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "wss":
		u.Scheme = "https"
	case "http", "ws":
		u.Scheme = "http"
	default:
		return "", fmt.Errorf("invalid server URL %q: scheme must be https or http", s)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("invalid server URL %q: missing host", s)
	}
	u.Host = strings.ToLower(u.Host)
	u.User, u.RawQuery, u.Fragment, u.RawPath = nil, "", "", ""
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), protocol.ConnectPath)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

// ConnectURL returns the WebSocket URL of the control endpoint for a server.
func ConnectURL(server string) (string, error) {
	n, err := NormalizeServer(server)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(n)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path += protocol.ConnectPath
	return u.String(), nil
}

// localTarget is how the client reaches the local service.
type localTarget struct {
	addr       string // host:port
	tls        bool
	serverName string
}

func parseTarget(localURL string) (localTarget, error) {
	u, err := url.Parse(localURL)
	if err != nil || u.Host == "" {
		return localTarget{}, fmt.Errorf("invalid local address %q", localURL)
	}
	return localTarget{addr: u.Host, tls: u.Scheme == "https", serverName: u.Hostname()}, nil
}

func (lt localTarget) dial(ctx context.Context) (net.Conn, error) {
	d := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	if !lt.tls {
		return d.DialContext(ctx, "tcp", lt.addr)
	}
	td := &tls.Dialer{NetDialer: d, Config: &tls.Config{
		// Local development servers almost always use self-signed certificates.
		InsecureSkipVerify: true,
		ServerName:         lt.serverName,
		NextProtos:         []string{"http/1.1"},
	}}
	return td.DialContext(ctx, "tcp", lt.addr)
}

// hostOf returns the lowercase host name of a URL.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func firstLabel(host string) string {
	label, _, _ := strings.Cut(host, ".")
	return label
}

// friendlyDialError shortens net errors like
// "dial tcp [::1]:3000: connect: connection refused" to "connection refused".
func friendlyDialError(err error) string {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		msg := op.Err.Error()
		msg = strings.TrimPrefix(msg, "connect: ")
		return msg
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "cannot resolve " + dnsErr.Name
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	return err.Error()
}
