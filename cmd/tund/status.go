package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"tund/internal/api"
	"tund/internal/client"
)

func newStatusCmd(g *globals) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "List your online tunnels on all machines",
		Long: `List the tunnels of your account that are online right now, on this and on
every other machine: where they point, which machine runs them, how long
they have been up and when the server closes them (if it limits the
tunnel lifetime).`,
		Example: `  tund status
  tund status --json`,
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
			if s.token == "" {
				return notLoggedInError(s)
			}
			ctx, stop := signalContext()
			defer stop()
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			c := api.New(s.server, s.token)
			c.UserAgent = "tund/" + client.Version
			tunnels, err := c.Tunnels(ctx)
			if err != nil {
				if api.StatusOf(err) == 401 {
					return fmt.Errorf("%w\n\nLog in again with:\n  %s", err, loginHint(s))
				}
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"tunnels": tunnels})
			}
			self, _ := os.Hostname()
			printStatus(os.Stdout, tunnels, self, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the API response as JSON")
	return cmd
}

// printStatus renders the online tunnels as a table, grouped by machine.
func printStatus(w io.Writer, tunnels []api.Tunnel, self string, now time.Time) {
	if len(tunnels) == 0 {
		fmt.Fprintln(w, "No tunnels online. Start one with: tund http 3000")
		return
	}
	sort.SliceStable(tunnels, func(i, j int) bool {
		a, b := tunnels[i], tunnels[j]
		if a.Client.Hostname != b.Client.Hostname {
			// This machine first.
			if a.Client.Hostname == self || b.Client.Hostname == self {
				return a.Client.Hostname == self
			}
			return a.Client.Hostname < b.Client.Hostname
		}
		return a.Name < b.Name
	})
	machines, nodes := map[string]bool{}, map[string]bool{}
	for _, t := range tunnels {
		machines[t.Client.Hostname] = true
		if t.Node != "" {
			nodes[t.Node] = true
		}
	}
	showNode := len(nodes) > 1

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	head := "NAME\tURL\tLOCAL\tMACHINE\tUP\tCLOSES"
	if showNode {
		head += "\tNODE"
	}
	fmt.Fprintln(tw, head)
	for _, t := range tunnels {
		machine := orDash(t.Client.Hostname)
		if self != "" && t.Client.Hostname == self {
			machine += " (this one)"
		}
		up, closes := "-", "-"
		if started, err := time.Parse(time.RFC3339Nano, t.StartedAt); err == nil {
			up = shortDuration(now.Sub(started))
		}
		if expires, err := time.Parse(time.RFC3339Nano, t.ExpiresAt); err == nil {
			closes = "in " + shortDuration(expires.Sub(now))
		}
		row := strings.Join([]string{t.Name, t.URL, t.LocalAddr, machine, up, closes}, "\t")
		if showNode {
			row += "\t" + orDash(t.Node)
		}
		fmt.Fprintln(tw, row)
	}
	tw.Flush()
	fmt.Fprintf(w, "\n%s on %s\n", plural(len(tunnels), "tunnel"), plural(len(machines), "machine"))
}

// shortDuration renders "45s", "12m", "3h5m" or "2d4h".
func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		if m := int(d.Minutes()) % 60; m > 0 {
			return fmt.Sprintf("%dh%dm", int(d.Hours()), m)
		}
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if h := int(d.Hours()) % 24; h > 0 {
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, h)
	}
	return fmt.Sprintf("%dd", int(d.Hours())/24)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
