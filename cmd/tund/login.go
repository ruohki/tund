package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"tund/internal/client"
)

func newLoginCmd(g *globals) *cobra.Command {
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "login [server]",
		Short: "Log in through your browser and save an authtoken",
		Long: `Log in through your browser and save an authtoken for this machine.

tund shows a short code and opens the server's dashboard, where you check
that the code matches and approve. The token is created on this machine;
only its hash is sent to the server.

With a server URL, tund logs in to that (self-hosted) server and remembers
it, so every later command uses it. Without one it uses the current server
(--server, TUND_SERVER, the config file, or the default ` + orNone(client.DefaultServer) + `).`,
		Example: `  tund login                                   # log in to the current server
  tund login https://tund.example.com          # log in to a self-hosted server and use it from now on
  tund login --no-browser                      # just print the link (e.g. over SSH)
  tund login --authtoken tund_…                # save an existing token (scripts, CI)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				server, err := client.NormalizeServer(args[0])
				if err != nil {
					return err
				}
				if g.server != "" {
					if other, _ := client.NormalizeServer(g.server); other != server {
						return errors.New("the server argument and --server disagree; pass only one")
					}
				}
				g.server = server
			}
			cfg, p, err := g.load()
			if err != nil {
				return err
			}
			s, err := g.resolve(cfg)
			if err != nil {
				return err
			}

			if g.authtoken != "" {
				// Scripted setup: behave like `tund config add-authtoken`.
				cfg.Authtoken = strings.TrimSpace(g.authtoken)
				if s.serverSource != client.SourceDefault {
					cfg.Server = s.server
				}
				if err := cfg.Save(p); err != nil {
					return err
				}
				fmt.Printf("Authtoken for %s saved to %s\n", s.server, p)
				return nil
			}

			ctx, stop := signalContext()
			defer stop()
			d := client.NewDisplay(g.logMode)
			if cfg.Authtoken != "" {
				d.Info("This replaces the authtoken saved in " + p + ".")
			}
			if _, err := loginAndSave(ctx, cfg, p, s, d, noBrowser); err != nil {
				return err
			}
			d.Info("\nStart a tunnel with:  tund http 3000")
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "don't open a browser, only print the link")
	return cmd
}

func newLogoutCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the saved authtoken and server",
		Long: `Remove the saved authtoken and server from the config file. Later commands
use the default server (` + orNone(client.DefaultServer) + `) again.

The token stays valid on the server until you revoke it in the dashboard.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, p, err := g.load()
			if err != nil {
				return err
			}
			server, _ := client.ResolveServer("", "", cfg.Server)
			if n, err := client.NormalizeServer(server); err == nil {
				server = n
			}
			if cfg.Authtoken == "" && cfg.Server == "" {
				fmt.Printf("Not logged in (no authtoken saved in %s).\n", p)
			} else {
				hadToken := cfg.Authtoken != ""
				cfg.Authtoken, cfg.Server = "", ""
				if err := cfg.Save(p); err != nil {
					return err
				}
				if hadToken {
					fmt.Printf("Logged out of %s: removed the authtoken and server from %s.\n", server, p)
					fmt.Printf("The token stays valid until you revoke it in the dashboard: %s/authtokens\n", server)
				} else {
					fmt.Printf("Removed the saved server %s from %s.\n", server, p)
				}
				if def, _ := client.NormalizeServer(client.DefaultServer); def != "" && def != server {
					fmt.Printf("Later commands use the default server %s.\n", def)
				}
			}
			for _, env := range []string{"TUND_AUTHTOKEN", "TUND_SERVER"} {
				if os.Getenv(env) != "" {
					fmt.Printf("Note: %s is set in your environment and still applies.\n", env)
				}
			}
			return nil
		},
	}
}

// loginAndSave runs the device flow and stores the new token (plus the server,
// unless it is the built-in default) in the config file.
func loginAndSave(ctx context.Context, cfg *client.Config, cfgPath string, s settings, d *client.Display, noBrowser bool) (string, error) {
	res, err := client.Login(ctx, client.LoginOptions{Server: s.server, NoBrowser: noBrowser, Display: d})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", errors.New("login cancelled")
		}
		return "", err
	}
	cfg.Authtoken = res.Token
	if s.serverSource != client.SourceDefault {
		cfg.Server = res.Server
	}
	if err := cfg.Save(cfgPath); err != nil {
		return "", fmt.Errorf("logged in, but saving the authtoken to %s failed: %w", cfgPath, err)
	}
	d.LoggedIn(res.Account, res.Server, cfgPath)
	if os.Getenv("TUND_AUTHTOKEN") != "" {
		d.Warn("TUND_AUTHTOKEN is set in your environment and overrides the saved token")
	}
	return res.Token, nil
}

// loginHint is the command that logs in to the server currently in use.
func loginHint(s settings) string {
	if s.serverSource == client.SourceFlag || s.serverSource == client.SourceEnv {
		return "tund login " + s.server
	}
	return "tund login"
}

// offerRelogin handles a rejected authtoken during `tund http` / `tund start`.
func offerRelogin(ctx context.Context, cfg *client.Config, cfgPath string, s settings, d *client.Display, authErr *client.AuthError) (string, error) {
	hint := fmt.Errorf("%w\n\nLog in again with:\n  %s", authErr, loginHint(s))
	switch {
	case s.tokenSource == client.SourceFlag:
		return "", fmt.Errorf("%w (token from --authtoken)", authErr)
	case s.tokenSource == client.SourceEnv:
		return "", fmt.Errorf("%w (token from TUND_AUTHTOKEN)", authErr)
	case !client.Interactive():
		return "", hint
	}
	d.Warn(authErr.Error())
	if !confirm(ctx, "Log in again now? [Y/n] ") {
		return "", hint
	}
	return loginAndSave(ctx, cfg, cfgPath, s, d, false)
}

// confirm asks a yes/no question on the terminal; Enter means yes.
func confirm(ctx context.Context, prompt string) bool {
	fmt.Print(prompt)
	answer := make(chan string, 1)
	go func() {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			line = "n"
		}
		answer <- line
	}()
	select {
	case <-ctx.Done():
		fmt.Println()
		return false
	case a := <-answer:
		a = strings.ToLower(strings.TrimSpace(a))
		return a == "" || a == "y" || a == "yes"
	}
}
