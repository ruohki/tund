package main

import (
	"errors"
	"net"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"tund/internal/client"
	"tund/internal/protocol"
)

func addAllowIPFlag(f *pflag.FlagSet, dst *[]string) {
	f.StringSliceVar(dst, "allow-ip", nil, "only these IPs or CIDR ranges may connect, e.g. 203.0.113.7,10.0.0.0/8 (repeatable)")
}

func addRestartFlag(f *pflag.FlagSet, dst *bool) {
	f.BoolVar(dst, "restart-on-expiry", false, "start the tunnel again when the server closes it for reaching its maximum lifetime")
}

// runSpec validates a single tunnel given on the command line and runs it.
func runSpec(g *globals, spec client.TunnelSpec) error {
	ips, err := client.NormalizeAllowIPs(spec.AllowIPs)
	if err != nil {
		return err
	}
	spec.AllowIPs = ips
	if err := spec.Validate(); err != nil {
		return err
	}
	cfg, p, err := g.load()
	if err != nil {
		return err
	}
	return runTunnels(g, cfg, p, []client.TunnelSpec{spec})
}

func portName(prefix, hostport string) string {
	if _, port, err := net.SplitHostPort(hostport); err == nil {
		return prefix + "-" + port
	}
	return prefix
}

func newTCPCmd(g *globals) *cobra.Command {
	var (
		name         string
		remotePort   int
		pin, restart bool
		allowIPs     []string
	)
	cmd := &cobra.Command{
		Use:   "tcp <port | host:port>",
		Short: "Expose a local TCP service (database, SSH, game server, …) on a public port",
		Long: `Expose a local TCP service on a public port of the tund server, e.g. a
database, SSH or a game server. Visitors connect to the tcp://host:port
address that tund prints; bytes are forwarded unchanged.

Without --remote-port you get one of your reserved ports if one is free,
otherwise a random port, which tund remembers for this local address and
asks for again next time. --pin reserves the port for your account.

Protect it with --allow-ip: raw TCP has no password or login page.`,
		Example: `  tund tcp 5432                                   # local Postgres
  tund tcp 22 --allow-ip 203.0.113.7              # SSH, only from one address
  tund tcp 25565 --remote-port 20565 --pin        # a fixed, reserved public port
  tund tcp 192.168.1.10:3389                      # another machine on your network`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			local, err := client.ParseTCPAddr(args[0])
			if err != nil {
				return err
			}
			if name == "" {
				name = portName("tcp", local)
			}
			return runSpec(g, client.TunnelSpec{
				Name: name, Proto: protocol.ProtoTCP, LocalAddr: local,
				RemotePort: remotePort, Pin: pin, AllowIPs: allowIPs, RestartOnExpiry: restart,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "tunnel name shown in the dashboard (default tcp-<port>)")
	f.IntVar(&remotePort, "remote-port", 0, "public port to use (must be free and within the server's TCP port range)")
	f.BoolVar(&pin, "pin", false, "reserve the resulting public port for your account")
	addAllowIPFlag(f, &allowIPs)
	addRestartFlag(f, &restart)
	return cmd
}

func newTLSCmd(g *globals) *cobra.Command {
	var (
		name, subdomain, domain, certFile, keyFile string
		pin, random, restart                       bool
		allowIPs                                   []string
	)
	cmd := &cobra.Command{
		Use:   "tls <port | host:port>",
		Short: "Expose a local TLS service by hostname, without decrypting it at the server",
		Long: `Expose a local service that speaks TLS on a public hostname. The server
routes the connection by its SNI and never decrypts it, so visitors see the
certificate of your local service (passthrough) — use a custom domain with
your own certificate, or let tund terminate TLS on this machine with
--terminate-cert/--terminate-key and forward plaintext to the local address.

Hostnames work like for tund http: your static hostname by default,
--random for a one-off one, --subdomain/--domain for a specific one.`,
		Example: `  tund tls 8443 --domain secure.example.com                                # passthrough
  tund tls 8080 --terminate-cert cert.pem --terminate-key key.pem         # TLS ends here
  tund tls 8443 --subdomain mqtt --pin --allow-ip 203.0.113.0/24`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			local, err := client.ParseTCPAddr(args[0])
			if err != nil {
				return err
			}
			if (certFile == "") != (keyFile == "") {
				return errors.New("--terminate-cert and --terminate-key must be used together")
			}
			if name == "" {
				name = portName("tls", local)
			}
			return runSpec(g, client.TunnelSpec{
				Name: name, Proto: protocol.ProtoTLS, LocalAddr: local,
				Subdomain: strings.ToLower(strings.TrimSpace(subdomain)),
				Hostname:  strings.ToLower(strings.TrimSpace(domain)),
				Pin:       pin, Random: random, AllowIPs: allowIPs,
				TerminateCert: certFile, TerminateKey: keyFile, RestartOnExpiry: restart,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "tunnel name shown in the dashboard (default tls-<port>)")
	f.StringVar(&subdomain, "subdomain", "", "subdomain of the server's base domain")
	f.StringVar(&domain, "domain", "", "full hostname, e.g. a custom domain verified in the dashboard")
	f.BoolVar(&pin, "pin", false, "keep the resulting hostname as a static hostname of your account")
	f.BoolVar(&random, "random", false, "use a one-off random hostname instead of your static one")
	f.StringVar(&certFile, "terminate-cert", "", "terminate TLS here with this certificate (PEM) and forward plaintext")
	f.StringVar(&keyFile, "terminate-key", "", "private key (PEM) for --terminate-cert")
	addAllowIPFlag(f, &allowIPs)
	addRestartFlag(f, &restart)
	return cmd
}
