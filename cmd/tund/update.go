package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"tund/internal/client"
	"tund/internal/protocol"
)

func newUpdateCmd(g *globals) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Install the latest tund release",
		Long: `Replace this tund binary with the latest release. It is downloaded through
the server you use (so self-hosted servers can mirror it), checked against the
published SHA-256 checksum and run once before it replaces the current one.

Running tunnels keep going with the old version until you restart them.`,
		Example: `  tund update            # install the latest release if this one is older
  tund update --force    # reinstall, e.g. over a development build`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := g.load()
			if err != nil {
				return err
			}
			s, err := g.resolve(cfg)
			if err != nil {
				return err
			}
			ctx, stop := signalContext()
			defer stop()
			res, err := client.Update(ctx, client.UpdateOptions{
				Server:   s.server,
				Force:    force,
				Progress: func(msg string) { fmt.Println(msg) },
			})
			var perm *client.PermissionError
			switch {
			case errors.As(err, &perm):
				retry := "sudo tund update"
				if s.serverSource != client.SourceDefault {
					retry += " --server " + s.server // root has its own config
				}
				return fmt.Errorf("%w\n\nRun it with sudo:\n  %s", err, retry)
			case err != nil:
				return err
			case !res.Updated:
				fmt.Printf("tund %s is up to date (latest release: %s).\n", res.From, res.To)
			default:
				fmt.Printf("Updated tund %s → %s (%s)\n", res.From, res.To, res.Path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "reinstall even if this version is current or a development build")
	return cmd
}

func newVersionCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the client version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("tund %s (%s/%s, protocol %s)\n", client.Version, runtime.GOOS, runtime.GOARCH, protocol.Version)
			// Only on a terminal: scripts (like the installer) parse this output.
			if term.IsTerminal(int(os.Stdout.Fd())) {
				if latest := latestRelease(g); protocol.NewerVersion(latest, client.Version) {
					fmt.Printf("\ntund %s is available. Update with: tund update\n", latest)
				}
			}
		},
	}
}

// latestRelease asks the current server for the newest release, briefly;
// "" when that doesn't work.
func latestRelease(g *globals) string {
	cfg, _, err := g.load()
	if err != nil {
		return ""
	}
	s, err := g.resolve(cfg)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	v, _ := client.LatestVersion(ctx, s.server, nil)
	return v
}
