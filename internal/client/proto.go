package client

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"tund/internal/protocol"
)

// ParseTCPAddr normalizes the local address of a tcp/tls tunnel:
// "5432" → "localhost:5432", "db:5432" and "tcp://db:5432" → "db:5432".
func ParseTCPAddr(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("missing local address")
	}
	if rest, ok := strings.CutPrefix(strings.ToLower(s), "tcp://"); ok {
		s = s[len(s)-len(rest):]
	} else if strings.Contains(s, "://") {
		return "", fmt.Errorf("invalid local address %q: tcp and tls tunnels take a port or host:port, not a URL", s)
	}
	if isDigits(s) {
		if err := checkPort(s); err != nil {
			return "", err
		}
		return "localhost:" + s, nil
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", fmt.Errorf("invalid local address %q: expected a port or host:port", s)
	}
	if host == "" {
		host = "localhost"
	}
	if err := checkPort(port); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, port), nil
}

// NormalizeAllowIPs validates IP and CIDR entries (entries may be comma
// separated) and returns them in canonical form.
func NormalizeAllowIPs(entries []string) ([]string, error) {
	var out []string
	for _, e := range entries {
		for _, part := range strings.Split(e, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if strings.Contains(part, "/") {
				p, err := netip.ParsePrefix(part)
				if err != nil {
					return nil, fmt.Errorf("invalid IP range %q in the IP allow list (use e.g. 203.0.113.0/24)", part)
				}
				out = append(out, p.Masked().String())
				continue
			}
			a, err := netip.ParseAddr(part)
			if err != nil {
				return nil, fmt.Errorf("invalid IP address %q in the IP allow list", part)
			}
			out = append(out, a.String())
		}
	}
	return out, nil
}

// proto returns the tunnel protocol, defaulting to http.
func (s TunnelSpec) proto() string {
	if s.Proto == "" {
		return protocol.ProtoHTTP
	}
	return s.Proto
}

// Validate checks that the options fit the tunnel's protocol.
func (s TunnelSpec) Validate() error {
	named := s.Subdomain != "" || s.Hostname != ""
	if s.Subdomain != "" && s.Hostname != "" {
		return errors.New("use either a subdomain or a domain, not both")
	}
	if s.Random && (named || s.RemotePort != 0) {
		return errors.New("random cannot be combined with a subdomain, domain or remote port")
	}
	if (s.TerminateCert == "") != (s.TerminateKey == "") {
		return errors.New("TLS termination needs both a certificate and a key")
	}
	if s.RemotePort < 0 || s.RemotePort > 65535 {
		return fmt.Errorf("invalid remote port %d", s.RemotePort)
	}
	switch s.proto() {
	case protocol.ProtoHTTP:
		if s.RemotePort != 0 {
			return errors.New("a remote port only applies to tcp tunnels")
		}
		if s.TerminateCert != "" {
			return errors.New("TLS termination only applies to tls tunnels")
		}
	case protocol.ProtoTCP:
		switch {
		case named || s.Random:
			return errors.New("tcp tunnels get a public port, not a hostname: use a remote port instead of subdomain/domain/random")
		case s.HostHeader != "":
			return errors.New("host header rewriting only applies to http tunnels")
		case s.Auth != nil:
			return errors.New("password and OIDC protection only apply to http tunnels; use an IP allow list for tcp")
		case s.TerminateCert != "":
			return errors.New("TLS termination only applies to tls tunnels")
		}
	case protocol.ProtoTLS:
		switch {
		case s.RemotePort != 0:
			return errors.New("a remote port only applies to tcp tunnels")
		case s.HostHeader != "":
			return errors.New("host header rewriting only applies to http tunnels")
		case s.Auth != nil:
			return errors.New("password and OIDC protection only apply to http tunnels; use an IP allow list for tls")
		}
	default:
		return fmt.Errorf("unknown protocol %q (use http, tcp or tls)", s.Proto)
	}
	return nil
}

// terminationConfig loads the certificate used to terminate TLS at the client.
func terminationConfig(certFile, keyFile string) (*tls.Config, error) {
	if certFile == "" {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("loading the TLS certificate: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

// targetFor returns how to reach the local service of a spec.
func targetFor(s TunnelSpec) (localTarget, error) {
	if s.proto() == protocol.ProtoHTTP {
		return parseTarget(s.LocalAddr)
	}
	host, _, err := net.SplitHostPort(s.LocalAddr)
	if err != nil {
		return localTarget{}, fmt.Errorf("invalid local address %q", s.LocalAddr)
	}
	return localTarget{addr: s.LocalAddr, serverName: host}, nil
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
