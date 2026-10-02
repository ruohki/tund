package client

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"tund/internal/protocol"
)

type welcome struct {
	account, serverVersion, dashboardURL string
}

// Display renders client output: an ngrok-style header plus a request log
// when stdout is a terminal, plain log lines otherwise.
type Display struct {
	mu      sync.Mutex
	out     io.Writer
	pretty  bool
	color   bool
	fd      int
	multi   bool
	nameW   int
	stopped bool
	said    map[string]bool
	update  string // newer release announced by the server
	header  bool   // the session summary has been printed
}

// NewDisplay picks the output style. forceLog selects plain log lines even on a terminal.
func NewDisplay(forceLog bool) *Display {
	fd := int(os.Stdout.Fd())
	tty := term.IsTerminal(fd)
	d := &Display{out: os.Stdout, fd: fd, pretty: tty && !forceLog, said: map[string]bool{}}
	if d.pretty && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" {
		d.color = enableVT(os.Stdout)
	}
	return d
}

func (d *Display) setTunnelNames(names []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.multi = len(names) > 1
	for _, n := range names {
		d.nameW = max(d.nameW, min(utf8.RuneCountInString(n), 16))
	}
}

// ANSI helpers.
const (
	reset   = "\x1b[0m"
	bold    = "\x1b[1m"
	dim     = "\x1b[2m"
	red     = "\x1b[31m"
	green   = "\x1b[32m"
	yellow  = "\x1b[33m"
	blue    = "\x1b[34m"
	magenta = "\x1b[35m"
	cyan    = "\x1b[36m"
)

func (d *Display) c(style, s string) string {
	if !d.color || s == "" {
		return s
	}
	return style + s + reset
}

func (d *Display) width() int {
	if w, _, err := term.GetSize(d.fd); err == nil && w > 20 {
		return w
	}
	return 100
}

func (d *Display) println(s string) {
	fmt.Fprintln(d.out, s)
}

// logf prints "<RFC3339 time> <event> key=value …".
func (d *Display) logf(event string, kv ...string) {
	var b strings.Builder
	b.WriteString(time.Now().Format(time.RFC3339))
	b.WriteByte(' ')
	b.WriteString(event)
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			continue
		}
		b.WriteByte(' ')
		b.WriteString(kv[i])
		b.WriteByte('=')
		v := kv[i+1]
		if strings.ContainsAny(v, " \t\"=") {
			v = strconv.Quote(v)
		}
		b.WriteString(v)
	}
	d.println(b.String())
}

// Connecting is shown before each connection attempt.
func (d *Display) Connecting(server string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.pretty {
		d.logf("connecting", "server", server)
		return
	}
	if !d.said["connecting"] {
		d.said["connecting"] = true
		d.println(d.c(dim, "Connecting to "+server+" …"))
	}
}

// Online is called when the server has accepted the session.
func (d *Display) Online(w welcome) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.pretty {
		d.logf("session online", "account", w.account, "server_version", w.serverVersion)
	}
}

// Reconnecting reports a lost or failed connection.
func (d *Display) Reconnecting(err error, delay time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	msg := "connection failed"
	if err != nil {
		msg = err.Error()
	}
	if !d.pretty {
		d.logf("reconnecting", "error", msg, "in", delay.Round(100*time.Millisecond).String())
		return
	}
	d.println(d.c(yellow, fmt.Sprintf("● %s — reconnecting in %s", msg, delay.Round(time.Second))))
}

// Stopped is shown on Ctrl+C.
func (d *Display) Stopped() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.stopped = true
	if !d.pretty {
		d.logf("stopped")
		return
	}
	d.println("")
	d.println(d.c(dim, "tunnels closed, bye"))
}

// Warn prints a non-fatal problem.
func (d *Display) Warn(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.pretty {
		d.logf("warning", "msg", msg)
		return
	}
	d.println(d.c(yellow, "warning: "+msg))
}

// UpdateAvailable notes a newer client release: a row in the session summary,
// or a line of its own once the summary is out.
func (d *Display) UpdateAvailable(version string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.update == version {
		return // announced again after a reconnect
	}
	d.update = version
	if !d.pretty {
		d.logf("update available", "version", version, "current", Version, "run", "tund update")
		return
	}
	if d.header {
		d.println(d.c(yellow, "tund "+version+" is available") + d.c(dim, " · update with: tund update"))
	}
}

// Info prints a neutral message.
func (d *Display) Info(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.pretty {
		d.logf("info", "msg", strings.TrimSpace(msg))
		return
	}
	d.println(msg)
}

func (d *Display) authBadge(mode string) string {
	switch mode {
	case protocol.AuthPassword:
		return d.c(magenta, "[password]")
	case protocol.AuthOIDC:
		return d.c(magenta, "[oidc]")
	}
	return ""
}

func (d *Display) row(label, value string) string {
	return d.c(dim, fmt.Sprintf("%-14s", label)) + value
}

func (d *Display) forwarding(t tunnelView) string {
	name := ""
	if d.multi {
		name = d.c(bold, padRight(truncate(t.Name, d.nameW), d.nameW)) + "  "
	}
	line := name + d.c(bold, t.URL) + d.c(dim, " → ") + t.Local
	if t.Static {
		line += "  " + d.c(cyan, "[static]")
	}
	if t.Proto == protocol.ProtoTLS {
		if t.Terminated {
			line += "  " + d.c(cyan, "[terminated]")
		} else {
			line += "  " + d.c(cyan, "[passthrough]")
		}
	}
	if badge := d.authBadge(t.AuthMode); badge != "" {
		line += "  " + badge
	}
	return line
}

func boolKV(b bool) string {
	if b {
		return "true"
	}
	return ""
}

func (d *Display) warningRow(t tunnelView) {
	if t.Warning != "" {
		d.println(d.row("", d.c(yellow, "⚠ "+t.Warning)))
	}
	if !t.ExpiresAt.IsZero() {
		note := "closes " + formatClock(t.ExpiresAt) + " (in " + formatRemaining(time.Until(t.ExpiresAt)) + ", maximum tunnel lifetime)"
		if t.RestartOnExpiry {
			note += " · restarts automatically"
		} else {
			note += " · --restart-on-expiry starts it again"
		}
		d.println(d.row("", d.c(dim, note)))
	}
}

// formatClock shows a time of day, with the date when it isn't today.
func formatClock(t time.Time) string {
	t = t.Local()
	if y, m, dd := t.Date(); time.Now().Format("2006-01-02") != fmt.Sprintf("%04d-%02d-%02d", y, m, dd) {
		return t.Format("Mon 2 Jan 15:04")
	}
	return "at " + t.Format("15:04")
}

// formatRemaining renders a time left as "45m", "2h", "1h30m" or "3d4h".
func formatRemaining(d time.Duration) string {
	m := int((d + time.Minute - 1) / time.Minute)
	switch {
	case m < 1:
		return "less than a minute"
	case m < 60:
		return fmt.Sprintf("%dm", m)
	case m < 1440:
		if m%60 == 0 {
			return fmt.Sprintf("%dh", m/60)
		}
		return fmt.Sprintf("%dh%dm", m/60, m%60)
	}
	if h := (m % 1440) / 60; h > 0 {
		return fmt.Sprintf("%dd%dh", m/1440, h)
	}
	return fmt.Sprintf("%dd", m/1440)
}

func expiresKV(t tunnelView) string {
	if t.ExpiresAt.IsZero() {
		return ""
	}
	return t.ExpiresAt.UTC().Format(time.RFC3339)
}

// browserWarningHint explains the warning page once per process.
func (d *Display) browserWarningHint(ts ...tunnelView) {
	if d.said["browser_warning"] {
		return
	}
	for _, t := range ts {
		if t.Online && t.BrowserWarning {
			d.said["browser_warning"] = true
			d.println(d.row("", d.c(dim, "Browser visitors see a one-time tund warning page first · skip it with the header "+protocol.HeaderSkipWarning+": 1")))
			return
		}
	}
}

// inspectHost is the host filter for the inspector (http tunnels only).
func (t tunnelView) inspectHost() string {
	if t.Proto == protocol.ProtoHTTP || t.Proto == "" {
		return t.Host
	}
	return ""
}

func inspectURL(dashboard string, host string) string {
	if host == "" {
		return dashboard + "/inspect"
	}
	return dashboard + "/inspect?host=" + url.QueryEscape(host)
}

// Header prints the session summary once all binds are resolved.
func (d *Display) Header(w welcome, ts []tunnelView) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.pretty {
		for _, t := range ts {
			switch {
			case t.Online:
				d.logf("tunnel online", "name", t.Name, "url", t.URL, "local", t.Local, "auth", t.AuthMode, "static", boolKV(t.Static), "browser_warning", boolKV(t.BrowserWarning), "warning", t.Warning, "expires_at", expiresKV(t), "inspect", inspectURL(w.dashboardURL, t.inspectHost()))
			case t.Failed:
				d.logf("tunnel failed", "name", t.Name, "local", t.Local, "error", t.Err)
			}
		}
		return
	}
	var online []tunnelView
	for _, t := range ts {
		if t.Online {
			online = append(online, t)
		}
	}
	d.println("")
	d.println(d.c(bold+cyan, "tund") + "  " + d.c(dim, "(Ctrl+C to quit)"))
	d.println("")
	status := d.c(green, "online")
	if len(online) == 0 {
		status = d.c(red, "no tunnels")
	}
	d.println(d.row("Session", status))
	if w.account != "" {
		d.println(d.row("Account", w.account))
	}
	version := Version
	if w.serverVersion != "" {
		version += d.c(dim, " (server "+w.serverVersion+")")
	}
	d.println(d.row("Version", version))
	if d.update != "" {
		d.println(d.row("Update", d.c(yellow, d.update+" available")+d.c(dim, " · run tund update")))
	}
	d.header = true
	if len(online) == 1 {
		d.println(d.row("Inspector", inspectURL(w.dashboardURL, online[0].inspectHost())))
	} else {
		d.println(d.row("Inspector", inspectURL(w.dashboardURL, "")))
	}
	for _, t := range ts {
		switch {
		case t.Online:
			d.println(d.row("Forwarding", d.forwarding(t)))
			d.warningRow(t)
		case t.Failed:
			d.println(d.row("Failed", d.c(red, t.Name+": "+t.Err)+d.c(dim, "  ("+t.Local+")")))
		}
	}
	d.browserWarningHint(ts...)
	d.println("")
	title := sectionTitle(ts)
	d.println(d.c(bold, title))
	d.println(d.c(dim, strings.Repeat("─", utf8.RuneCountInString(title))))
}

func sectionTitle(ts []tunnelView) string {
	http, raw := false, false
	for _, t := range ts {
		if t.Proto == protocol.ProtoTCP || t.Proto == protocol.ProtoTLS {
			raw = true
		} else {
			http = true
		}
	}
	switch {
	case http && raw:
		return "Requests & Connections"
	case raw:
		return "Connections"
	}
	return "HTTP Requests"
}

// TunnelOnline announces a tunnel that came up after the header was printed.
func (d *Display) TunnelOnline(t tunnelView) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.pretty {
		d.logf("tunnel online", "name", t.Name, "url", t.URL, "local", t.Local, "auth", t.AuthMode, "static", boolKV(t.Static), "browser_warning", boolKV(t.BrowserWarning), "warning", t.Warning, "expires_at", expiresKV(t))
		return
	}
	d.println(d.row("Forwarding", d.forwarding(t)))
	d.warningRow(t)
	d.browserWarningHint(t)
}

// TunnelFailed announces a tunnel that could not be bound.
func (d *Display) TunnelFailed(t tunnelView) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.pretty {
		d.logf("tunnel failed", "name", t.Name, "local", t.Local, "error", t.Err)
		return
	}
	d.println(d.c(red, fmt.Sprintf("✗ %s (%s): %s", t.Name, t.Local, t.Err)))
}

// TunnelClosed announces a tunnel stopped from the dashboard.
func (d *Display) TunnelClosed(t tunnelView) {
	d.mu.Lock()
	defer d.mu.Unlock()
	reason := t.Err
	if reason == "" {
		reason = "closed by the server"
	}
	if !d.pretty {
		d.logf("tunnel closed", "name", t.Name, "url", t.URL, "reason", reason)
		return
	}
	d.println(d.c(yellow, fmt.Sprintf("■ %s closed: %s", t.URL, reason)))
}

// TunnelRestarting announces a tunnel bound again after reaching the
// maximum lifetime (--restart-on-expiry).
func (d *Display) TunnelRestarting(t tunnelView) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.pretty {
		d.logf("tunnel restarting", "name", t.Name, "url", t.URL, "reason", "maximum lifetime reached")
		return
	}
	d.println(d.c(yellow, fmt.Sprintf("↻ %s reached the maximum tunnel lifetime; starting it again", t.URL)))
}

// Request logs one proxied request.
func (d *Display) Request(t tunnelView, ev protocol.RequestEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	status := strconv.Itoa(ev.Status)
	if ev.Status == 0 {
		status = "ERR"
	}
	text := http.StatusText(ev.Status)
	if !d.pretty {
		kv := []string{"method", ev.Method, "path", ev.Path, "status", status, "duration", formatDuration(ev.DurationMS), "remote", ev.RemoteAddr, "error", ev.Error}
		if d.multi {
			kv = append([]string{"tunnel", t.Name}, kv...)
		}
		d.logf("request", kv...)
		return
	}

	stColor := green
	switch {
	case ev.Status == 0 || ev.Status >= 500:
		stColor = red
	case ev.Status >= 400:
		stColor = yellow
	case ev.Status >= 300:
		stColor = cyan
	}
	d.logLine(t, ev.Method, ev.Path, strings.TrimSpace(status+" "+text), stColor, ev.DurationMS, ev.Error)
}

// Connection logs one finished TCP/TLS connection.
func (d *Display) Connection(t tunnelView, ev protocol.ConnEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.pretty {
		kv := []string{"remote", ev.RemoteAddr, "in", strconv.FormatInt(ev.BytesIn, 10), "out", strconv.FormatInt(ev.BytesOut, 10),
			"duration", formatDuration(ev.DurationMS), "error", ev.Error}
		if d.multi {
			kv = append([]string{"tunnel", t.Name}, kv...)
		}
		d.logf("connection", kv...)
		return
	}
	color := green
	if ev.Error != "" {
		color = red
	}
	traffic := "↑" + humanBytes(ev.BytesIn) + " ↓" + humanBytes(ev.BytesOut)
	d.logLine(t, strings.ToUpper(t.Proto), ev.RemoteAddr, traffic, color, ev.DurationMS, ev.Error)
}

// logLine prints "time  [tunnel]  METHOD  path  status  duration  error".
func (d *Display) logLine(t tunnelView, method, path, status, statusColor string, durationMS float64, errMsg string) {
	const methodW, statusW, durW = 7, 24, 8
	nameCol := ""
	nameW := 0
	if d.multi {
		nameCol = padRight(truncate(t.Name, d.nameW), d.nameW) + "  "
		nameW = d.nameW + 2
	}
	pathW := d.width() - (10 + nameW + methodW + 1 + 2 + statusW + durW + 1)
	pathW = max(16, min(pathW, 90))
	line := d.c(dim, time.Now().Format("15:04:05")) + "  " +
		d.c(dim, nameCol) +
		d.c(bold, fmt.Sprintf("%-*s", methodW, truncate(method, methodW))) + " " +
		padRight(truncate(path, pathW), pathW) + "  " +
		d.c(statusColor, padRight(truncate(status, statusW), statusW)) +
		fmt.Sprintf("%*s", durW, formatDuration(durationMS))
	if errMsg != "" {
		line += "  " + d.c(red, errMsg)
	}
	d.println(line)
}

func formatDuration(ms float64) string {
	switch {
	case ms < 1:
		return fmt.Sprintf("%.1fms", ms)
	case ms < 1000:
		return fmt.Sprintf("%.0fms", ms)
	case ms < 60000:
		return fmt.Sprintf("%.2fs", ms/1000)
	}
	d := time.Duration(ms) * time.Millisecond
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func padRight(s string, n int) string {
	if l := utf8.RuneCountInString(s); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}
