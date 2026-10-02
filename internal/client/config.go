package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"tund/internal/protocol"
)

// Version is set at build time via -ldflags "-X tund/internal/client.Version=…".
var Version = "dev"

// DefaultServer is used when neither --server, TUND_SERVER nor the config file
// name a server. Forks can change it via -ldflags "-X tund/internal/client.DefaultServer=…".
var DefaultServer = "https://tund.io"

// Where a setting came from.
const (
	SourceFlag    = "flag"
	SourceEnv     = "env"
	SourceConfig  = "config"
	SourceDefault = "default"
	SourceNone    = ""
)

// Pick returns the first non-empty value in precedence order flag > env >
// config > default, and where it came from.
func Pick(flagVal, envVal, cfgVal, defVal string) (string, string) {
	for _, c := range []struct{ v, src string }{
		{flagVal, SourceFlag}, {envVal, SourceEnv}, {cfgVal, SourceConfig}, {defVal, SourceDefault},
	} {
		if v := strings.TrimSpace(c.v); v != "" {
			return v, c.src
		}
	}
	return "", SourceNone
}

// ResolveServer applies the server precedence including DefaultServer.
func ResolveServer(flagVal, envVal, cfgVal string) (string, string) {
	return Pick(flagVal, envVal, cfgVal, DefaultServer)
}

// Config is the YAML configuration file of the client.
type Config struct {
	Server    string                   `yaml:"server,omitempty"`
	Authtoken string                   `yaml:"authtoken,omitempty"`
	Tunnels   map[string]*TunnelConfig `yaml:"tunnels,omitempty"`
}

// TunnelConfig is a named tunnel that `tund start` can run.
type TunnelConfig struct {
	Addr          string      `yaml:"addr"`
	Subdomain     string      `yaml:"subdomain,omitempty"`
	Domain        string      `yaml:"domain,omitempty"`
	HostHeader    string      `yaml:"host_header,omitempty"`
	Auth          *AuthConfig `yaml:"auth,omitempty"`
	Pin           bool        `yaml:"pin,omitempty"`
	Random        bool        `yaml:"random,omitempty"`
	Proto         string      `yaml:"proto,omitempty"`       // http (default), tcp or tls
	RemotePort    int         `yaml:"remote_port,omitempty"` // tcp
	AllowIPs      []string    `yaml:"allow_ips,omitempty"`
	TerminateCert string      `yaml:"terminate_cert,omitempty"` // tls
	TerminateKey  string      `yaml:"terminate_key,omitempty"`  // tls
	// RestartOnExpiry starts the tunnel again when the server closes it for
	// reaching its maximum lifetime.
	RestartOnExpiry bool `yaml:"restart_on_expiry,omitempty"`

	// Pool shares the hostname with your other tunnels that use pool (http).
	Pool bool `yaml:"pool,omitempty"`

	// Traffic rules (http): see protocol.Rules.
	RequestHeaders  *HeaderRulesConfig `yaml:"request_headers,omitempty"`
	ResponseHeaders *HeaderRulesConfig `yaml:"response_headers,omitempty"`
	CORS            *CORSConfig        `yaml:"cors,omitempty"`
	RateLimit       string             `yaml:"rate_limit,omitempty"` // e.g. 100/m per visitor IP
	Routes          []RouteConfig      `yaml:"routes,omitempty"`
}

// AuthConfig protects a tunnel with a password or an OIDC provider.
type AuthConfig struct {
	Password string   `yaml:"password,omitempty"`
	OIDC     string   `yaml:"oidc,omitempty"`
	Allow    []string `yaml:"allow,omitempty"`
}

// DefaultConfigPath is ~/.config/tund/tund.yml (or $XDG_CONFIG_HOME) on Unix
// and %AppData%\tund\tund.yml on Windows.
func DefaultConfigPath() (string, error) {
	if runtime.GOOS == "windows" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "tund", "tund.yml"), nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "tund", "tund.yml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tund", "tund.yml"), nil
}

// LoadConfig reads the config file; a missing file yields an empty config.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the config atomically with 0600 permissions.
func (c *Config) Save(path string) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append([]byte("# tund client configuration\n"), b...))
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tund-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o600)
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		os.Remove(tmp)
	}
	return werr
}

// NormalizeAllow cleans one OIDC allow-list entry: emails ("a@b.com") and
// domains ("@b.com") are lowercased, "group:<name>" keeps its case because
// groups match exactly.
func NormalizeAllow(entry string) string {
	entry = strings.TrimSpace(entry)
	if name, ok := strings.CutPrefix(entry, "group:"); ok {
		if name = strings.TrimSpace(name); name != "" {
			return "group:" + name
		}
		return ""
	}
	return strings.ToLower(entry)
}

// BuildAuth turns CLI/config options into a protocol access policy.
func BuildAuth(password, oidc string, allow []string) (*protocol.Auth, error) {
	var clean []string
	for _, a := range allow {
		for _, part := range strings.Split(a, ",") {
			if part = NormalizeAllow(part); part != "" {
				clean = append(clean, part)
			}
		}
	}
	switch {
	case password != "" && oidc != "":
		return nil, errors.New("use either a password or OIDC, not both")
	case password != "":
		if len(clean) > 0 {
			return nil, errors.New("an allow list only applies to OIDC")
		}
		return &protocol.Auth{Mode: protocol.AuthPassword, Password: password}, nil
	case oidc != "":
		return &protocol.Auth{Mode: protocol.AuthOIDC, Provider: oidc, Allow: clean}, nil
	case len(clean) > 0:
		return nil, errors.New("an allow list needs an OIDC provider (--oidc <slug>)")
	}
	return nil, nil
}

// ParseTarget normalizes a local address for a protocol: http takes a port,
// host:port or http(s) URL; tcp and tls take a port or host:port.
func ParseTarget(proto, addr string) (string, error) {
	switch proto {
	case "", protocol.ProtoHTTP:
		return ParseLocalAddr(addr)
	case protocol.ProtoTCP, protocol.ProtoTLS:
		return ParseTCPAddr(addr)
	}
	return "", fmt.Errorf("unknown protocol %q (use http, tcp or tls)", proto)
}

// Spec converts a configured tunnel into a runnable TunnelSpec. Relative
// certificate paths are resolved against baseDir (the config file's
// directory) when it is not empty.
func (t *TunnelConfig) Spec(name string) (TunnelSpec, error) { return t.specIn(name, "") }

func (t *TunnelConfig) specIn(name, baseDir string) (TunnelSpec, error) {
	if t == nil || strings.TrimSpace(t.Addr) == "" {
		return TunnelSpec{}, fmt.Errorf("tunnel %q: missing addr", name)
	}
	proto := strings.ToLower(strings.TrimSpace(t.Proto))
	local, err := ParseTarget(proto, t.Addr)
	if err != nil {
		return TunnelSpec{}, fmt.Errorf("tunnel %q: %w", name, err)
	}
	spec := TunnelSpec{
		Name:          name,
		Proto:         proto,
		LocalAddr:     local,
		Subdomain:     strings.ToLower(strings.TrimSpace(t.Subdomain)),
		Hostname:      strings.ToLower(strings.TrimSpace(t.Domain)),
		HostHeader:    t.HostHeader,
		Pin:           t.Pin,
		Random:        t.Random,
		RemotePort:    t.RemotePort,
		TerminateCert: resolvePath(baseDir, t.TerminateCert),
		TerminateKey:  resolvePath(baseDir, t.TerminateKey),

		RestartOnExpiry: t.RestartOnExpiry,
		Pool:            t.Pool,
	}
	if spec.AllowIPs, err = NormalizeAllowIPs(t.AllowIPs); err != nil {
		return TunnelSpec{}, fmt.Errorf("tunnel %q: %w", name, err)
	}
	if t.Auth != nil {
		if spec.Auth, err = BuildAuth(t.Auth.Password, t.Auth.OIDC, t.Auth.Allow); err != nil {
			return TunnelSpec{}, fmt.Errorf("tunnel %q: %w", name, err)
		}
	}
	if spec.Rules, err = BuildRules(RuleOptions{
		RequestHeaders: t.RequestHeaders, ResponseHeaders: t.ResponseHeaders,
		CORS: t.CORS, RateLimit: t.RateLimit, RouteConfigs: t.Routes,
	}); err != nil {
		return TunnelSpec{}, fmt.Errorf("tunnel %q: %w", name, err)
	}
	if err := spec.Validate(); err != nil {
		return TunnelSpec{}, fmt.Errorf("tunnel %q: %w", name, err)
	}
	return spec, nil
}

// TunnelSpec returns the named tunnel of a config loaded from path.
func (c *Config) TunnelSpec(name, configPath string) (TunnelSpec, error) {
	t, ok := c.Tunnels[name]
	if !ok {
		return TunnelSpec{}, fmt.Errorf("no tunnel %q", name)
	}
	return t.specIn(name, filepath.Dir(configPath))
}

func resolvePath(baseDir, p string) string {
	p = strings.TrimSpace(p)
	switch {
	case p == "":
		return ""
	case strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`):
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	case baseDir != "" && !filepath.IsAbs(p):
		return filepath.Join(baseDir, p)
	}
	return p
}

// State remembers random subdomains between runs so the public URL (and its
// certificate) stays the same for a local address.
type State struct {
	mu         sync.Mutex
	path       string
	Subdomains map[string]string `json:"subdomains"`
	Ports      map[string]int    `json:"ports,omitempty"` // tcp tunnels
}

// StatePath returns the state file next to the config file.
func StatePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "state.json")
}

// LoadState never fails; a missing or corrupt file yields an empty state.
func LoadState(path string) *State {
	s := &State{path: path, Subdomains: map[string]string{}, Ports: map[string]int{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, s)
		if s.Subdomains == nil {
			s.Subdomains = map[string]string{}
		}
		if s.Ports == nil {
			s.Ports = map[string]int{}
		}
	}
	return s
}

func (s *State) Get(key string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Subdomains[key]
}

func (s *State) Set(key, label string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if label == "" {
		if _, ok := s.Subdomains[key]; !ok {
			return nil
		}
		delete(s.Subdomains, key)
	} else {
		if s.Subdomains[key] == label {
			return nil
		}
		s.Subdomains[key] = label
	}
	return s.save()
}

// GetPort returns the remembered public port of a tcp tunnel.
func (s *State) GetPort(key string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Ports[key]
}

// SetPort remembers (or, with 0, forgets) the public port of a tcp tunnel.
func (s *State) SetPort(key string, port int) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Ports[key] == port || (port == 0 && s.Ports[key] == 0) {
		return nil
	}
	if port == 0 {
		delete(s.Ports, key)
	} else {
		s.Ports[key] = port
	}
	return s.save()
}

// save writes the state; the caller holds s.mu.
func (s *State) save() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, b)
}
