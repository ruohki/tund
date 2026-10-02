package client

import (
	"errors"
	"fmt"
	"strings"

	"tund/internal/protocol"
)

// featureServerVersion is the first server release that understands traffic
// rules; older servers would silently ignore them.
const featureServerVersion = "0.5.0"

// HeaderRulesConfig sets and removes headers (tund.yml).
type HeaderRulesConfig struct {
	Set    map[string]string `yaml:"set,omitempty"`
	Remove []string          `yaml:"remove,omitempty"`
}

// CORSConfig lets the server answer CORS for the tunnel (tund.yml).
type CORSConfig struct {
	Origins     []string `yaml:"origins"`
	Methods     []string `yaml:"methods,omitempty"`
	Headers     []string `yaml:"headers,omitempty"`
	Expose      []string `yaml:"expose,omitempty"`
	Credentials bool     `yaml:"credentials,omitempty"`
	MaxAge      int      `yaml:"max_age,omitempty"`
}

// RouteConfig sends a path prefix to another local address (tund.yml).
type RouteConfig struct {
	Path        string `yaml:"path"`
	Addr        string `yaml:"addr"`
	StripPrefix bool   `yaml:"strip_prefix,omitempty"`
}

// RuleOptions are the traffic rules of an HTTP tunnel as given on the
// command line or in tund.yml.
type RuleOptions struct {
	RequestSet, ResponseSet       []string // "Name: value"
	RequestRemove, ResponseRemove []string
	RequestHeaders                *HeaderRulesConfig
	ResponseHeaders               *HeaderRulesConfig
	CORSOrigins                   []string
	CORS                          *CORSConfig
	RateLimit                     string
	Routes                        []string // "/api=8080"
	RouteConfigs                  []RouteConfig
}

func splitList(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func headerRules(kind string, set, remove []string, cfg *HeaderRulesConfig) (*protocol.HeaderRules, error) {
	hr := &protocol.HeaderRules{Set: map[string]string{}}
	if cfg != nil {
		for k, v := range cfg.Set {
			hr.Set[strings.TrimSpace(k)] = v
		}
		hr.Remove = append(hr.Remove, splitList(cfg.Remove)...)
	}
	for _, s := range set {
		name, value, ok := strings.Cut(s, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("invalid %s header %q: use \"Name: value\"", kind, s)
		}
		hr.Set[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	hr.Remove = append(hr.Remove, splitList(remove)...)
	if len(hr.Set) == 0 && len(hr.Remove) == 0 {
		return nil, nil
	}
	if len(hr.Set) == 0 {
		hr.Set = nil
	}
	return hr, nil
}

// BuildRules turns rule options into protocol rules; nil when there are none.
func BuildRules(o RuleOptions) (*protocol.Rules, error) {
	r := &protocol.Rules{RateLimit: strings.TrimSpace(o.RateLimit)}
	var err error
	if r.RequestHeaders, err = headerRules("request", o.RequestSet, o.RequestRemove, o.RequestHeaders); err != nil {
		return nil, err
	}
	if r.ResponseHeaders, err = headerRules("response", o.ResponseSet, o.ResponseRemove, o.ResponseHeaders); err != nil {
		return nil, err
	}
	if c := o.CORS; c != nil || len(o.CORSOrigins) > 0 {
		r.CORS = &protocol.CORS{Origins: splitList(o.CORSOrigins)}
		if c != nil {
			r.CORS.Origins = append(r.CORS.Origins, splitList(c.Origins)...)
			r.CORS.Methods, r.CORS.Headers, r.CORS.Expose = splitList(c.Methods), splitList(c.Headers), splitList(c.Expose)
			r.CORS.Credentials, r.CORS.MaxAge = c.Credentials, c.MaxAge
		}
		if len(r.CORS.Origins) == 0 {
			return nil, errors.New("CORS needs at least one origin, e.g. https://app.example.com or *")
		}
	}
	routes := o.RouteConfigs
	for _, s := range o.Routes {
		path, addr, ok := strings.Cut(s, "=")
		if !ok {
			return nil, fmt.Errorf("invalid route %q: use /path=port, e.g. /api=8080", s)
		}
		routes = append(routes, RouteConfig{Path: path, Addr: addr})
	}
	for _, rc := range routes {
		path := strings.TrimSpace(rc.Path)
		if !strings.HasPrefix(path, "/") || strings.TrimRight(path, "/") == "" {
			return nil, fmt.Errorf("invalid route path %q: use a path prefix like /api", rc.Path)
		}
		local, err := ParseLocalAddr(rc.Addr)
		if err != nil {
			return nil, fmt.Errorf("route %s: %w", path, err)
		}
		r.Routes = append(r.Routes, protocol.Route{Path: path, LocalAddr: local, StripPrefix: rc.StripPrefix})
	}
	if r.RequestHeaders == nil && r.ResponseHeaders == nil && r.CORS == nil && r.RateLimit == "" && len(r.Routes) == 0 {
		return nil, nil
	}
	return r, nil
}

// rulesSummary describes the rules in one line for the terminal.
func rulesSummary(r *protocol.Rules) string {
	if r == nil {
		return ""
	}
	var parts []string
	n := 0
	for _, hr := range []*protocol.HeaderRules{r.RequestHeaders, r.ResponseHeaders} {
		if hr != nil {
			n += len(hr.Set) + len(hr.Remove)
		}
	}
	if n == 1 {
		parts = append(parts, "1 header rule")
	} else if n > 1 {
		parts = append(parts, fmt.Sprintf("%d header rules", n))
	}
	if r.CORS != nil {
		parts = append(parts, "CORS "+strings.Join(r.CORS.Origins, ", "))
	}
	if r.RateLimit != "" {
		parts = append(parts, "rate limit "+r.RateLimit+" per visitor")
	}
	return strings.Join(parts, " · ")
}
