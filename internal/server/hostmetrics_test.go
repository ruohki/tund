package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseCPUStat(t *testing.T) {
	busy, total := parseCPUStat("cpu  100 5 50 800 20 3 2 10 0 0\ncpu0 50 2 25 400 10 1 1 5 0 0\n")
	if total != 990 || busy != 170 {
		t.Fatalf("busy=%d total=%d, want 170/990", busy, total)
	}
	if b, tot := parseCPUStat("intr 1 2 3"); b != 0 || tot != 0 {
		t.Fatalf("no cpu line: %d/%d", b, tot)
	}
}

func TestParseMeminfo(t *testing.T) {
	total, avail, ok := parseMeminfo("MemTotal:        3906776 kB\nMemFree:          770000 kB\nMemAvailable:    2978000 kB\n")
	if !ok || total != 3906776*1024 || avail != 2978000*1024 {
		t.Fatalf("total=%d avail=%d ok=%v", total, avail, ok)
	}
	if _, _, ok := parseMeminfo("MemTotal: 1 kB\n"); ok {
		t.Fatal("ok without MemAvailable")
	}
}

const netDev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 5000      50    0    0    0     0          0         0     5000      50    0    0    0     0       0          0
  eth0: 1000000   900    0    0    0     0          0         0  2000000    800    0    0    0     0       0          0
enp7s0: 300000    100    0    0    0     0          0         0   400000    100    0    0    0     0       0          0
docker0: 70000     10    0    0    0     0          0         0    70000     10    0    0    0     0       0          0
br-97cad2cce6ec: 9000 1 0 0 0 0 0 0 9000 1 0 0 0 0 0 0
`

func TestNetDev(t *testing.T) {
	ifaces := parseNetDev(netDev)
	if got := ifaces["eth0"]; got != [2]uint64{1000000, 2000000} {
		t.Fatalf("eth0 = %v", got)
	}
	if in, out := sumNetDev(ifaces, "eth0"); in != 1000000 || out != 2000000 {
		t.Fatalf("public interface: %d/%d", in, out)
	}
	// Unknown public interface: everything except loopback and Docker's.
	if in, out := sumNetDev(ifaces, ""); in != 1300000 || out != 2400000 {
		t.Fatalf("all physical: %d/%d", in, out)
	}
}

func TestHostCollectorRates(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("stat", "cpu  100 0 100 800 0 0 0 0 0 0\n")
	write("loadavg", "0.42 0.30 0.20 1/123 4567\n")
	write("meminfo", "MemTotal: 1000 kB\nMemAvailable: 250 kB\n")
	write("net/dev", "  eth9: 1000 1 0 0 0 0 0 0 4000 1 0 0 0 0 0 0\n")

	h := &hostCollector{procDir: dir, capacityMbps: 1000}
	first := h.collect(0, 2, 3)
	if first.CPUPct != nil || first.NetInRate != nil || first.TunnelRate != nil {
		t.Fatal("first sample must not report rates")
	}
	if first.Load1 == nil || *first.Load1 != 0.42 || *first.MemUsed != 750*1024 || first.Sessions != 2 || first.Tunnels != 3 {
		t.Fatalf("first sample: %+v", first)
	}

	h.prev.at = h.prev.at.Add(-10 * time.Second) // pretend ten seconds passed
	write("stat", "cpu  150 0 150 900 0 0 0 0 0 0\n")
	write("net/dev", "  eth9: 11000 1 0 0 0 0 0 0 24000 1 0 0 0 0 0 0\n")
	m := h.collect(50_000, 2, 3)
	if m.CPUPct == nil || *m.CPUPct < 49.9 || *m.CPUPct > 50.1 {
		t.Fatalf("cpu = %v, want 50%%", m.CPUPct)
	}
	if m.NetInRate == nil || *m.NetInRate < 990 || *m.NetInRate > 1010 || *m.NetOutRate < 1990 || *m.NetOutRate > 2010 {
		t.Fatalf("net rates %v/%v, want ~1000/2000", *m.NetInRate, *m.NetOutRate)
	}
	if m.TunnelRate == nil || *m.TunnelRate < 4990 || *m.TunnelRate > 5010 {
		t.Fatalf("tunnel rate %v, want ~5000", *m.TunnelRate)
	}
}

// Runs against a real database: TUND_TEST_DATABASE_URL=postgres://… go test ./internal/server -run NodeMetrics
func TestNodeMetricsPostgres(t *testing.T) {
	dsn := os.Getenv("TUND_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TUND_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := OpenStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	defer st.pool.Exec(ctx, `delete from nodes where name = 'test-metrics'`)

	n := nodeInfo{Name: "test-metrics", Role: "edge", Region: "eu-central", RelayURL: "10.0.0.9:4443", CertSHA: "abc", Version: "test"}
	if err := st.UpsertNode(ctx, n, nodeMetrics{PublicIP: "203.0.113.9", CPUs: 2}); err != nil {
		t.Fatal(err)
	}
	cpu, rate := 42.5, int64(1234)
	if err := st.UpsertNode(ctx, n, nodeMetrics{PublicIP: "203.0.113.9", CapacityMbps: 1000, CPUs: 2, CPUPct: &cpu,
		NetOutRate: &rate, Sessions: 4, Tunnels: 7}); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range nodes {
		if got.Name != n.Name {
			continue
		}
		m := got.Metrics
		if m.PublicIP != "203.0.113.9" || m.CapacityMbps != 1000 || m.CPUPct == nil || *m.CPUPct != cpu ||
			m.NetOutRate == nil || *m.NetOutRate != rate || m.NetInRate != nil || m.Sessions != 4 || m.Tunnels != 7 || got.MetricsAt == nil {
			t.Fatalf("metrics = %+v at %v", m, got.MetricsAt)
		}
		return
	}
	t.Fatal("node not found")
}
