package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"tund/internal/client"
)

func newConfigCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage the client configuration file",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "add-authtoken <token>",
			Short: "Save an authtoken created in the dashboard",
			Long: `Save an authtoken created in the dashboard. ` + "`tund login`" + ` is usually easier;
this is meant for scripts and machines without a browser.`,
			Example: "  tund config add-authtoken tund_0123456789abcdef0123456789abcdef01234567",
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, p, err := g.load()
				if err != nil {
					return err
				}
				tok := strings.TrimSpace(args[0])
				if !strings.HasPrefix(tok, "tund_") {
					fmt.Fprintln(os.Stderr, "warning: tund authtokens normally start with \"tund_\"")
				}
				cfg.Authtoken = tok
				if g.server != "" || os.Getenv("TUND_SERVER") != "" {
					s, err := g.resolve(cfg)
					if err != nil {
						return err
					}
					cfg.Server = s.server
				}
				if err := cfg.Save(p); err != nil {
					return err
				}
				fmt.Println("Authtoken saved to " + p)
				return nil
			},
		},
		&cobra.Command{
			Use:   "set-server <url>",
			Short: "Use a self-hosted tund server",
			Long: `Use a self-hosted tund server for all later commands.
` + "`tund login <url>`" + ` does the same and logs you in at the same time.`,
			Example: "  tund config set-server https://tund.example.com",
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				server, err := client.NormalizeServer(args[0])
				if err != nil {
					return err
				}
				cfg, p, err := g.load()
				if err != nil {
					return err
				}
				previous, _ := client.ResolveServer("", "", cfg.Server)
				previous, _ = client.NormalizeServer(previous)
				cfg.Server = server
				if err := cfg.Save(p); err != nil {
					return err
				}
				fmt.Printf("Server set to %s in %s\n", server, p)
				if cfg.Authtoken != "" && previous != server {
					fmt.Printf("Your saved authtoken is for %s; run `tund login` if the new server rejects it.\n", previous)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "show",
			Short: "Show the effective configuration and where each value comes from",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, p, err := g.load()
				if err != nil {
					return err
				}
				server, serverSrc := client.ResolveServer(g.server, os.Getenv("TUND_SERVER"), cfg.Server)
				if n, err := client.NormalizeServer(server); err == nil {
					server = n
				}
				token, tokenSrc := client.Pick(g.authtoken, os.Getenv("TUND_AUTHTOKEN"), cfg.Authtoken, "")
				fmt.Printf("%-12s %s\n", "Config file", p)
				fmt.Printf("%-12s %s  (%s)\n", "Server", orNone(server), describeSource(serverSrc, "--server", "TUND_SERVER"))
				if token == "" {
					fmt.Printf("%-12s %s\n", "Authtoken", "(not set — run `tund login`)")
				} else {
					fmt.Printf("%-12s %s  (%s)\n", "Authtoken", maskToken(token), describeSource(tokenSrc, "--authtoken", "TUND_AUTHTOKEN"))
				}
				names := tunnelNames(cfg)
				if len(names) == 0 {
					fmt.Printf("%-12s %s\n", "Tunnels", "(none)")
				}
				for i, n := range names {
					label := ""
					if i == 0 {
						label = "Tunnels"
					}
					fmt.Printf("%-12s %s\n", label, describeTunnel(n, cfg.Tunnels[n]))
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "path",
			Short: "Print the config file path",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				p, err := g.path()
				if err != nil {
					return err
				}
				fmt.Println(p)
				return nil
			},
		},
	)
	return cmd
}

func maskToken(t string) string {
	if t == "" {
		return ""
	}
	if len(t) <= 13 {
		return strings.Repeat("•", len(t))
	}
	return t[:13] + strings.Repeat("•", 8)
}

func describeTunnel(name string, t *client.TunnelConfig) string {
	if t == nil {
		return name + " (empty)"
	}
	parts := []string{t.Addr}
	if t.Proto != "" && t.Proto != "http" {
		parts = []string{t.Proto + " " + t.Addr}
	}
	if t.RemotePort != 0 {
		parts = append(parts, fmt.Sprintf("remote port %d", t.RemotePort))
	}
	if len(t.AllowIPs) > 0 {
		parts = append(parts, "allow "+strings.Join(t.AllowIPs, ","))
	}
	if t.TerminateCert != "" {
		parts = append(parts, "terminated")
	}
	if t.Subdomain != "" {
		parts = append(parts, "subdomain "+t.Subdomain)
	}
	if t.Domain != "" {
		parts = append(parts, "domain "+t.Domain)
	}
	if t.Random {
		parts = append(parts, "random")
	}
	if t.Pin {
		parts = append(parts, "pin")
	}
	if t.Auth != nil {
		switch {
		case t.Auth.Password != "":
			parts = append(parts, "password")
		case t.Auth.OIDC != "":
			parts = append(parts, "oidc "+t.Auth.OIDC)
		}
	}
	return name + " (" + strings.Join(parts, ", ") + ")"
}
