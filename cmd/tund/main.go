// Command tund exposes local HTTP services on public HTTPS URLs through a
// tund server (the hosted service by default, or a self-hosted instance).
package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"tund/internal/client"
	"tund/internal/protocol"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "tund: "+err.Error())
		os.Exit(1)
	}
}

type globals struct {
	server     string
	authtoken  string
	configPath string
	logMode    bool
}

func (g *globals) path() (string, error) {
	if g.configPath != "" {
		return g.configPath, nil
	}
	return client.DefaultConfigPath()
}

func (g *globals) load() (*client.Config, string, error) {
	p, err := g.path()
	if err != nil {
		return nil, "", err
	}
	cfg, err := client.LoadConfig(p)
	return cfg, p, err
}

// settings are the effective server and authtoken with their sources.
type settings struct {
	server, serverSource string
	token, tokenSource   string
}

// resolve applies the precedence flag > environment > config file > built-in default.
func (g *globals) resolve(cfg *client.Config) (settings, error) {
	var s settings
	s.server, s.serverSource = client.ResolveServer(g.server, os.Getenv("TUND_SERVER"), cfg.Server)
	s.token, s.tokenSource = client.Pick(g.authtoken, os.Getenv("TUND_AUTHTOKEN"), cfg.Authtoken, "")
	if s.server == "" {
		return s, errors.New("no server configured\n\nLog in to your tund server:\n  tund login https://your-server")
	}
	var err error
	if s.server, err = client.NormalizeServer(s.server); err != nil {
		return s, fmt.Errorf("%w (from %s)", err, describeSource(s.serverSource, "--server", "TUND_SERVER"))
	}
	return s, nil
}

func describeSource(src, flag, env string) string {
	switch src {
	case client.SourceFlag:
		return flag
	case client.SourceEnv:
		return env
	case client.SourceConfig:
		return "config file"
	case client.SourceDefault:
		return "default"
	}
	return "not set"
}

// signalContext is cancelled on Ctrl+C / SIGTERM.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func newRootCmd() *cobra.Command {
	g := &globals{}
	root := &cobra.Command{
		Use:   "tund",
		Short: "Expose local servers on public HTTPS URLs",
		Long: `tund gives a service running on this machine a public HTTPS URL, with every
request recorded in the dashboard. It only makes an outgoing connection
(WebSocket over HTTPS), so it works behind NAT and firewalls and needs no
admin rights or network drivers.

Get started:
  tund http 3000                       # the first run logs you in via your browser

Self-hosting? Log in to your own server once; every later command uses it:
  tund login https://tund.example.com

Default server: ` + orNone(client.DefaultServer),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&g.server, "server", "", "tund server URL for this run (env TUND_SERVER)")
	root.PersistentFlags().StringVar(&g.authtoken, "authtoken", "", "authtoken for this run (env TUND_AUTHTOKEN)")
	root.PersistentFlags().StringVar(&g.configPath, "config", "", "config file (default: "+defaultPathHint()+")")
	root.PersistentFlags().BoolVar(&g.logMode, "log", false, "print plain log lines instead of the interactive view")

	root.AddCommand(newHTTPCmd(g), newTCPCmd(g), newTLSCmd(g), newStartCmd(g), newLoginCmd(g), newLogoutCmd(g), newMCPCmd(g), newConfigCmd(g), newVersionCmd())
	return root
}

func defaultPathHint() string {
	if p, err := client.DefaultConfigPath(); err == nil {
		return p
	}
	return "~/.config/tund/tund.yml"
}

func newHTTPCmd(g *globals) *cobra.Command {
	var (
		name, subdomain, domain, hostHeader, password, oidc string
		allow, allowIPs                                     []string
		pin, random                                         bool
	)
	cmd := &cobra.Command{
		Use:   "http <port | host:port | url>",
		Short: "Expose a local HTTP(S) service on a public HTTPS URL",
		Long: `Expose a local HTTP(S) service on a public HTTPS URL.

The first time you run it, tund opens your browser to log you in.

By default you get your account's static URL: the same hostname every time,
on any machine (it is created on first use). Use --random for a one-off URL,
or --subdomain myapp --pin to claim another static hostname and keep it.

With --password or --oidc, visitors must sign in first and your app receives
who they are in request headers: X-Tund-Auth (oidc|password) and, for OIDC,
X-Tund-User-Email, -Name, -Username, -Id and -Groups. tund removes any
X-Tund-* headers sent by visitors, so your app can trust them.`,
		Example: `  tund http 3000                                  # your static URL → http://localhost:3000
  tund http 3000 --random                         # a one-off random URL
  tund http 3000 --subdomain myapp --pin          # claim https://myapp.<base domain> and keep it
  tund http 192.168.1.20:8080                     # another machine on your network
  tund http https://localhost:8443                # local HTTPS (self-signed is fine)
  tund http 3000 --domain api.example.com         # a custom domain verified in the dashboard
  tund http 3000 --host-header rewrite            # send Host: localhost:3000 upstream
  tund http 3000 --allow-ip 203.0.113.0/24        # only this network may connect
  tund http 3000 --password s3cret                # visitors must enter a password
  tund http 3000 --oidc google --oidc-allow @example.com,bob@gmail.com
  tund http 3000 --oidc acme/okta --oidc-allow group:engineering   # a provider of team "acme"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			local, err := client.ParseLocalAddr(args[0])
			if err != nil {
				return err
			}
			if subdomain != "" && domain != "" {
				return errors.New("use either --subdomain or --domain, not both")
			}
			if random && (subdomain != "" || domain != "") {
				return errors.New("--random cannot be combined with --subdomain or --domain")
			}
			auth, err := client.BuildAuth(password, oidc, allow)
			if err != nil {
				return err
			}
			if name == "" {
				name = defaultName(local)
			}
			spec := client.TunnelSpec{
				Name:       name,
				LocalAddr:  local,
				Subdomain:  strings.ToLower(strings.TrimSpace(subdomain)),
				Hostname:   strings.ToLower(strings.TrimSpace(domain)),
				HostHeader: hostHeader,
				Auth:       auth,
				Pin:        pin,
				Random:     random,
				AllowIPs:   allowIPs,
			}
			return runSpec(g, spec)
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "tunnel name shown in the dashboard (default http-<port>)")
	f.StringVar(&subdomain, "subdomain", "", "subdomain of the server's base domain")
	f.StringVar(&domain, "domain", "", "full hostname, e.g. a custom domain verified in the dashboard")
	f.BoolVar(&pin, "pin", false, "keep the resulting hostname as a static hostname of your account")
	f.BoolVar(&random, "random", false, "use a one-off random hostname instead of your static one")
	f.StringVar(&hostHeader, "host-header", "", `Host header sent upstream: "preserve" (default), "rewrite" or a literal value`)
	f.StringVar(&password, "password", "", "protect the URL with a password")
	f.StringVar(&oidc, "oidc", "", "protect the URL with an OIDC provider from the dashboard: <provider> or <team>/<provider>")
	f.StringSliceVar(&allow, "oidc-allow", nil, "who may pass OIDC: emails (a@b.com), domains (@b.com) or groups (group:admins); repeatable or comma separated")
	addAllowIPFlag(f, &allowIPs)
	return cmd
}

func defaultName(local string) string {
	u, err := url.Parse(local)
	if err != nil {
		return "http"
	}
	return u.Scheme + "-" + u.Port()
}

func newStartCmd(g *globals) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "start [name ...]",
		Short: "Start tunnels defined in the config file",
		Long: `Start one or more tunnels defined under "tunnels:" in the config file
(see ` + "`tund config path`" + `).

Example config:

  tunnels:
    web:
      addr: 3000
      subdomain: myapp
      pin: true            # keep myapp as a static hostname
    preview:
      addr: 5173
      random: true         # one-off URL, not your static one
    api:
      addr: https://localhost:8443
      domain: api.example.com
      host_header: rewrite
      auth:
        oidc: acme/okta      # <provider> or <team>/<provider>
        allow: ["@example.com", "group:engineering"]
    db:
      proto: tcp
      addr: 5432
      remote_port: 20432
      allow_ips: ["203.0.113.0/24"]
    mqtt:
      proto: tls
      addr: 8883
      terminate_cert: certs/mqtt.pem   # relative to this file
      terminate_key: certs/mqtt-key.pem
    admin:
      addr: 9000
      auth:
        password: s3cret`,
		Example: `  tund start web api
  tund start --all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, p, err := g.load()
			if err != nil {
				return err
			}
			if len(cfg.Tunnels) == 0 {
				return fmt.Errorf("no tunnels defined in %s (see `tund start --help` for an example)", p)
			}
			names := args
			if all {
				if len(args) > 0 {
					return errors.New("use either --all or tunnel names")
				}
				names = tunnelNames(cfg)
			}
			if len(names) == 0 {
				return fmt.Errorf("name the tunnels to start or pass --all; defined in %s: %s", p, strings.Join(tunnelNames(cfg), ", "))
			}
			var specs []client.TunnelSpec
			for _, n := range names {
				if _, ok := cfg.Tunnels[n]; !ok {
					return fmt.Errorf("no tunnel %q in %s; defined: %s", n, p, strings.Join(tunnelNames(cfg), ", "))
				}
				spec, err := cfg.TunnelSpec(n, p)
				if err != nil {
					return err
				}
				specs = append(specs, spec)
			}
			return runTunnels(g, cfg, p, specs)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "start every tunnel in the config file")
	return cmd
}

func tunnelNames(cfg *client.Config) []string {
	var out []string
	for n := range cfg.Tunnels {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func runTunnels(g *globals, cfg *client.Config, cfgPath string, specs []client.TunnelSpec) error {
	s, err := g.resolve(cfg)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	display := client.NewDisplay(g.logMode)

	if s.token == "" {
		if !client.Interactive() {
			return notLoggedInError(s)
		}
		display.Info("You're not logged in to " + s.server + ".")
		tok, err := loginAndSave(ctx, cfg, cfgPath, s, display, false)
		if err != nil {
			return err
		}
		s.token, s.tokenSource = tok, client.SourceConfig
	}

	state := client.LoadState(client.StatePath(cfgPath))
	for {
		c, err := client.New(client.Options{
			Server:    s.server,
			Authtoken: s.token,
			Tunnels:   specs,
			State:     state,
			Display:   display,
		})
		if err != nil {
			return err
		}
		err = c.Run(ctx)
		var authErr *client.AuthError
		switch {
		case errors.Is(err, client.ErrStopped):
			display.Info(err.Error())
			return nil
		case errors.As(err, &authErr):
			tok, lerr := offerRelogin(ctx, cfg, cfgPath, s, display, authErr)
			if lerr != nil {
				return lerr
			}
			s.token, s.tokenSource = tok, client.SourceConfig
			continue
		}
		return err
	}
}

func notLoggedInError(s settings) error {
	return fmt.Errorf("not logged in to %s\n\nRun `%s` in a terminal, or set TUND_AUTHTOKEN to a token from the dashboard (%s/authtokens).", s.server, loginHint(s), s.server)
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the client version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("tund %s (%s/%s, protocol %s)\n", client.Version, runtime.GOOS, runtime.GOARCH, protocol.Version)
		},
	}
}

func orNone(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}
