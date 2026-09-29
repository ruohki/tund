package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"tund/internal/client"
	tundmcp "tund/internal/mcp"
)

func newMCPCmd(g *globals) *cobra.Command {
	var (
		allowPorts      []string
		requirePassword bool
		noBrowser       bool
		debug           bool
	)
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP server so AI agents can expose what they build",
		Long: `Run a Model Context Protocol server on stdin/stdout, so AI agents (Claude Code,
Claude Desktop, Cursor, …) can put the app they are building on a public HTTPS
URL and look at the requests it receives.

Tools: whoami, login, start_tunnel, list_tunnels, stop_tunnel, list_requests,
get_request, replay_request.

It uses the normal client configuration (server and authtoken, see
` + "`tund config show`" + `). If you are not logged in yet, the agent calls the login
tool and shows you a link and a code to approve in your browser. Tunnels run
inside this process and stop when the agent session ends. Logs go to stderr;
stdout carries the protocol.

Claude Code:
  claude mcp add tund -- tund mcp
  claude mcp add tund -- tund mcp --allow-ports 3000,5173 --require-password

Claude Desktop (claude_desktop_config.json), Cursor (~/.cursor/mcp.json) and
most other clients:
  {
    "mcpServers": {
      "tund": { "command": "tund", "args": ["mcp"] }
    }
  }

Use the full path to tund (` + "`which tund`" + `) if the client does not find it.`,
		Example: `  tund mcp
  tund mcp --allow-ports 3000,5173       # agents may only expose these local ports
  tund mcp --require-password            # every tunnel gets a password
  tund mcp --server https://tund.example.com`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ports, err := parsePorts(allowPorts)
			if err != nil {
				return err
			}
			cfg, p, err := g.load()
			if err != nil {
				return err
			}
			s, err := g.resolve(cfg)
			if err != nil {
				return err
			}
			level := slog.LevelInfo
			if debug {
				level = slog.LevelDebug
			}
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
			logger.Info("tund mcp starting", "server", s.server, "logged_in", s.token != "", "version", client.Version)

			srv := tundmcp.New(tundmcp.Options{
				Server:          s.server,
				ServerSource:    s.serverSource,
				Token:           s.token,
				ConfigPath:      p,
				AllowPorts:      ports,
				RequirePassword: requirePassword,
				NoBrowser:       noBrowser,
				Logger:          logger,
				State:           client.LoadState(client.StatePath(p)),
			})
			ctx, stop := signalContext()
			defer stop()
			err = srv.Run(ctx, tundmcp.DrainingStdio())
			logger.Info("tund mcp stopped")
			return err
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&allowPorts, "allow-ports", nil, "only allow exposing these local ports (comma separated)")
	f.BoolVar(&requirePassword, "require-password", false, "give every tunnel a random password unless the agent sets one")
	f.BoolVar(&noBrowser, "no-browser", false, "never open a browser for login, even if the agent asks")
	f.BoolVar(&debug, "debug", false, "log every proxied request to stderr")
	return cmd
}

func parsePorts(vals []string) ([]int, error) {
	var out []int
	for _, v := range vals {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, err := strconv.Atoi(part)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("invalid port %q in --allow-ports", part)
			}
			out = append(out, n)
		}
	}
	return out, nil
}
