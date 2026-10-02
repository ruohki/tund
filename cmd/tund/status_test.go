package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"tund/internal/api"
)

func TestPrintStatus(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	tunnels := []api.Tunnel{
		{Name: "web", URL: "https://a.tund.io", LocalAddr: "http://localhost:3000", StartedAt: "2026-10-02T10:30:00Z",
			ExpiresAt: "2026-10-02T14:30:00Z", Node: "fsn-1", Client: api.TunnelClient{Hostname: "ci-runner"}},
		{Name: "api", URL: "https://b.tund.io", LocalAddr: "http://localhost:8080", StartedAt: "2026-10-02T11:59:15Z",
			Node: "hel-1", Client: api.TunnelClient{Hostname: "mbp"}},
	}
	var b bytes.Buffer
	printStatus(&b, tunnels, "mbp", now)
	out := b.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(lines[1], "api ") || !strings.Contains(lines[1], "mbp (this one)") {
		t.Fatalf("this machine should come first:\n%s", out)
	}
	for _, want := range []string{"NODE", "45s", "1h30m", "in 2h30m", "2 tunnels on 2 machines"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	b.Reset()
	printStatus(&b, tunnels[:1], "mbp", now)
	if strings.Contains(b.String(), "NODE") || !strings.Contains(b.String(), "1 tunnel on 1 machine") {
		t.Errorf("single node output:\n%s", b.String())
	}
	b.Reset()
	printStatus(&b, nil, "mbp", now)
	if !strings.Contains(b.String(), "No tunnels online") {
		t.Errorf("empty output: %q", b.String())
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second: "0s", 45 * time.Second: "45s", 12 * time.Minute: "12m", 3*time.Hour + 5*time.Minute: "3h5m",
		2 * time.Hour: "2h", 52 * time.Hour: "2d4h", 48 * time.Hour: "2d",
	} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
