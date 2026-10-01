package server

import (
	"bufio"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Host metrics a node reports with its heartbeat (docs/SPEC.md "Edge nodes").
// They come from /proc, so they describe the host on Linux (the server runs
// with host networking) and stay empty elsewhere.

type nodeMetrics struct {
	PublicIP     string
	CapacityMbps int
	CPUs         int
	CPUPct       *float64
	Load1        *float64
	MemTotal     *int64
	MemUsed      *int64
	NetInRate    *int64 // bytes per second
	NetOutRate   *int64
	TunnelRate   *int64
	Sessions     int
	Tunnels      int
}

type hostSample struct {
	at                time.Time
	cpuBusy, cpuTotal uint64
	netIn, netOut     uint64
	netOK             bool
	tunnelBytes       int64
}

type hostCollector struct {
	publicIP     string
	capacityMbps int
	procDir      string

	mu   sync.Mutex
	prev *hostSample
}

func newHostCollector(cfg *Config) *hostCollector {
	ip := cfg.PublicIP
	if ip == "" {
		ip = detectPublicIP()
	}
	return &hostCollector{publicIP: ip, capacityMbps: cfg.CapacityMbps, procDir: "/proc"}
}

// collect takes a sample and returns the metrics; rates cover the time since
// the previous call. tunnelBytes is the node's running total of tunnel traffic.
func (h *hostCollector) collect(tunnelBytes int64, sessions, tunnels int) nodeMetrics {
	m := nodeMetrics{PublicIP: h.publicIP, CapacityMbps: h.capacityMbps, CPUs: runtime.NumCPU(), Sessions: sessions, Tunnels: tunnels}
	cur := hostSample{at: time.Now(), tunnelBytes: tunnelBytes}
	if s, err := os.ReadFile(h.procDir + "/stat"); err == nil {
		cur.cpuBusy, cur.cpuTotal = parseCPUStat(string(s))
	}
	if s, err := os.ReadFile(h.procDir + "/loadavg"); err == nil {
		if v, ok := parseLoadAvg(string(s)); ok {
			m.Load1 = &v
		}
	}
	if s, err := os.ReadFile(h.procDir + "/meminfo"); err == nil {
		if total, avail, ok := parseMeminfo(string(s)); ok {
			used := total - avail
			m.MemTotal, m.MemUsed = &total, &used
		}
	}
	if s, err := os.ReadFile(h.procDir + "/net/dev"); err == nil {
		cur.netIn, cur.netOut = sumNetDev(parseNetDev(string(s)), publicInterface(h.publicIP))
		cur.netOK = true
	}

	h.mu.Lock()
	prev := h.prev
	h.prev = &cur
	h.mu.Unlock()
	if prev == nil {
		return m
	}
	secs := cur.at.Sub(prev.at).Seconds()
	if secs <= 0 {
		return m
	}
	if cur.cpuTotal > prev.cpuTotal && cur.cpuBusy >= prev.cpuBusy {
		v := 100 * float64(cur.cpuBusy-prev.cpuBusy) / float64(cur.cpuTotal-prev.cpuTotal)
		m.CPUPct = &v
	}
	rate := func(a, b uint64) *int64 {
		if a < b { // counter reset (interface re-created)
			return nil
		}
		v := int64(float64(a-b) / secs)
		return &v
	}
	if cur.netOK && prev.netOK {
		m.NetInRate, m.NetOutRate = rate(cur.netIn, prev.netIn), rate(cur.netOut, prev.netOut)
	}
	if cur.tunnelBytes >= prev.tunnelBytes {
		v := int64(float64(cur.tunnelBytes-prev.tunnelBytes) / secs)
		m.TunnelRate = &v
	}
	return m
}

// parseCPUStat returns busy and total jiffies from the "cpu" line of /proc/stat.
func parseCPUStat(s string) (busy, total uint64) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		// user nice system idle iowait irq softirq steal (guest time is already in user/nice)
		var v [8]uint64
		for i := 0; i < len(v) && i+1 < len(f); i++ {
			v[i], _ = strconv.ParseUint(f[i+1], 10, 64)
		}
		for _, x := range v {
			total += x
		}
		return total - v[3] - v[4], total
	}
	return 0, 0
}

func parseLoadAvg(s string) (float64, bool) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	return v, err == nil
}

// parseMeminfo returns MemTotal and MemAvailable in bytes.
func parseMeminfo(s string) (total, avail int64, ok bool) {
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		key, rest, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			total, haveTotal = kb*1024, true
		case "MemAvailable":
			avail, haveAvail = kb*1024, true
		}
	}
	return total, avail, haveTotal && haveAvail
}

// parseNetDev returns received and transmitted bytes per interface.
func parseNetDev(s string) map[string][2]uint64 {
	out := map[string][2]uint64{}
	for _, line := range strings.Split(s, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out[strings.TrimSpace(name)] = [2]uint64{rx, tx}
	}
	return out
}

// sumNetDev adds up the traffic of the interface carrying the public IP, or,
// when that isn't known (NAT), of every interface except loopback and the
// virtual ones Docker creates.
func sumNetDev(ifaces map[string][2]uint64, public string) (in, out uint64) {
	if v, ok := ifaces[public]; ok && public != "" {
		return v[0], v[1]
	}
	for name, v := range ifaces {
		if name == "lo" || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") {
			continue
		}
		in += v[0]
		out += v[1]
	}
	return in, out
}

// publicInterface names the interface that holds ip, if any.
func publicInterface(ip string) string {
	want := net.ParseIP(ip)
	if want == nil {
		return ""
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(want) {
				return iface.Name
			}
		}
	}
	return ""
}

// detectPublicIP returns the source address for outbound traffic when it is a
// public address (true on most VPSs; behind NAT, set TUND_PUBLIC_IP instead).
// The UDP "dial" only picks a route, nothing is sent.
func detectPublicIP() string {
	c, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	ip := c.LocalAddr().(*net.UDPAddr).IP
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return ""
	}
	return ip.String()
}
