package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	"tund/internal/tlsutil"
)

const (
	deviceCodePath  = "/_tund/device/code"
	deviceTokenPath = "/_tund/device/token"
)

// pollUnit scales the server's polling interval (seconds); tests shrink it.
var pollUnit = time.Second

var (
	ErrLoginDenied  = errors.New("the login request was denied in the browser")
	ErrLoginExpired = errors.New("the login code expired before it was approved; run `tund login` to get a new one")
)

// GenerateToken returns a new random authtoken: "tund_" + 40 hex characters.
func GenerateToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "tund_" + hex.EncodeToString(b), nil
}

// TokenHash is the sha256 hex digest the server stores for an authtoken.
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// TokenPrefix is the part of an authtoken shown in the dashboard.
func TokenPrefix(token string) string {
	if len(token) < 13 {
		return token
	}
	return token[:13]
}

// EndpointURL joins a server base URL and an edge path such as /_tund/device/code.
func EndpointURL(server, path string) (string, error) {
	n, err := NormalizeServer(server)
	if err != nil {
		return "", err
	}
	return n + path, nil
}

// Interactive reports whether both stdin and stdout are terminals.
func Interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// LoginOptions configure Login.
type LoginOptions struct {
	Server     string
	NoBrowser  bool
	Display    *Display
	HTTPClient *http.Client
}

// LoginResult is an approved device login.
type LoginResult struct {
	Server  string // normalized
	Token   string
	Account string
}

type deviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURL         string `json:"verification_url"`
	VerificationURLComplete string `json:"verification_url_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
	CallbackDoneURL         string `json:"callback_done_url"`
}

// DeviceLogin is a started device authorization: show UserCode and
// VerificationURL(Complete) to the user, then call Poll (or Wait).
type DeviceLogin struct {
	Server                  string // normalized
	UserCode                string
	VerificationURL         string
	VerificationURLComplete string
	ExpiresAt               time.Time

	token      string
	deviceCode string
	interval   time.Duration
	hc         *http.Client
	tokenURL   string
	cb         *callback // nil: plain device flow
}

// StartDeviceLogin creates a token locally and registers its hash with the
// server, which answers with the code the user has to approve.
func StartDeviceLogin(ctx context.Context, server string, hc *http.Client) (*DeviceLogin, error) {
	return startLogin(ctx, server, hc, nil)
}

// StartBrowserLogin is StartDeviceLogin for a browser on this machine: the
// CLI listens on 127.0.0.1 and the dashboard redirects there after the user
// approves, so there is no code to compare. Servers without callback support
// get a plain device login (Callback reports false). Call Wait to finish; it
// also closes the listener.
func StartBrowserLogin(ctx context.Context, server string, hc *http.Client) (*DeviceLogin, error) {
	cb, err := listenCallback()
	if err != nil {
		return startLogin(ctx, server, hc, nil) // no loopback here (sandbox?): fall back to the code
	}
	dl, err := startLogin(ctx, server, hc, cb)
	if err != nil || dl.cb == nil {
		cb.close()
	}
	return dl, err
}

// Callback reports whether the login finishes through the browser redirect to
// this machine rather than by comparing the code.
func (dl *DeviceLogin) Callback() bool { return dl.cb != nil }

func startLogin(ctx context.Context, server string, hc *http.Client, cb *callback) (*DeviceLogin, error) {
	server, err := NormalizeServer(server)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tlsutil.MustClientConfig()}}
	}
	token, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	hostname, _ := os.Hostname()
	codeURL, _ := EndpointURL(server, deviceCodePath)
	tokenURL, _ := EndpointURL(server, deviceTokenPath)

	req := map[string]any{
		"token_hash":      TokenHash(token),
		"token_prefix":    TokenPrefix(token),
		"client_hostname": hostname,
		"client_os":       runtime.GOOS + "/" + runtime.GOARCH,
	}
	if cb != nil {
		req["callback_port"], req["callback_state"] = cb.port(), cb.state
	}
	status, body, err := postJSON(ctx, hc, codeURL, req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("cannot reach %s: %w", server, unwrapDial(err))
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return nil, fmt.Errorf("%s does not support `tund login` (is it a tund server, and up to date?)\n\nCreate an authtoken in its dashboard instead and run:\n  tund config add-authtoken <token>", server)
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("too many login attempts from your network (%s); try again in a few minutes", apiErrorMessage(body, status))
	default:
		return nil, fmt.Errorf("%s could not start a login: %s", server, apiErrorMessage(body, status))
	}
	var dc deviceCode
	if err := json.Unmarshal(body, &dc); err != nil || dc.DeviceCode == "" || dc.UserCode == "" {
		return nil, fmt.Errorf("%s sent an invalid login response", server)
	}
	if dc.VerificationURL == "" {
		dc.VerificationURL = server + "/device"
	}
	if dc.VerificationURLComplete == "" {
		dc.VerificationURLComplete = dc.VerificationURL + "?code=" + url.QueryEscape(dc.UserCode)
	}
	interval := time.Duration(dc.Interval) * pollUnit
	if dc.Interval <= 0 {
		interval = 5 * pollUnit
	}
	expiresIn := time.Duration(dc.ExpiresIn) * pollUnit
	if dc.ExpiresIn <= 0 {
		expiresIn = 10 * pollUnit * 60
	}
	dl := &DeviceLogin{
		Server:                  server,
		UserCode:                dc.UserCode,
		VerificationURL:         dc.VerificationURL,
		VerificationURLComplete: dc.VerificationURLComplete,
		ExpiresAt:               time.Now().Add(expiresIn),
		token:                   token,
		deviceCode:              dc.DeviceCode,
		interval:                interval,
		hc:                      hc,
		tokenURL:                tokenURL,
	}
	// An older server ignores the callback fields and doesn't send
	// callback_done_url; its dashboard shows the code instead.
	if cb != nil && dc.CallbackDoneURL != "" {
		dl.cb = cb
		cb.start(dl, dc.CallbackDoneURL)
	}
	return dl, nil
}

// OpenBrowser tries to open the approval page; it reports whether it did.
func (dl *DeviceLogin) OpenBrowser() bool { return openBrowser(dl.VerificationURLComplete) }

// Poll waits until the login is approved, denied or expired. note, if not
// nil, receives transient status messages ("" clears them).
func (dl *DeviceLogin) Poll(ctx context.Context, note func(string)) (*LoginResult, error) {
	if note == nil {
		note = func(string) {}
	}
	interval := dl.interval
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		if time.Now().After(dl.ExpiresAt) {
			return nil, ErrLoginExpired
		}
		status, body, err := postJSON(ctx, dl.hc, dl.tokenURL, map[string]string{"device_code": dl.deviceCode})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			note("connection problem, retrying")
			continue
		}
		switch {
		case status == http.StatusOK:
			var ok struct {
				Status  string `json:"status"`
				Account string `json:"account"`
			}
			_ = json.Unmarshal(body, &ok)
			return &LoginResult{Server: dl.Server, Token: dl.token, Account: ok.Account}, nil
		case status == http.StatusPreconditionRequired: // authorization_pending
			note("")
		case status == http.StatusTooManyRequests: // slow down
			interval += 5 * pollUnit
			note("")
		case status == http.StatusForbidden:
			return nil, ErrLoginDenied
		case status == http.StatusGone, status == http.StatusNotFound:
			return nil, ErrLoginExpired
		case status >= 500:
			note("server error, retrying")
		default:
			return nil, fmt.Errorf("login failed: %s", apiErrorMessage(body, status))
		}
	}
}

// Wait is Poll that, for a callback login, also finishes when the browser
// arrives at the callback; whichever settles the login first wins. It closes
// the callback listener.
func (dl *DeviceLogin) Wait(ctx context.Context, note func(string)) (*LoginResult, error) {
	if dl.cb == nil {
		return dl.Poll(ctx, note)
	}
	defer dl.cb.close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	polled := make(chan callbackResult, 1)
	go func() {
		// The poll still settles denied and expired logins, and approvals
		// through a dashboard that predates the callback.
		res, err := dl.Poll(ctx, note)
		polled <- callbackResult{res, err}
	}()
	var r callbackResult
	select {
	case r = <-dl.cb.result:
	case r = <-polled:
	}
	return r.res, r.err
}

// redeem trades the callback code from the browser redirect for the token's
// activation. Errors that are not *callbackRetry end the login.
func (dl *DeviceLogin) redeem(ctx context.Context, code string) (*LoginResult, error) {
	status, body, err := postJSON(ctx, dl.hc, dl.tokenURL, map[string]string{"device_code": dl.deviceCode, "callback_code": code})
	if err != nil {
		return nil, &callbackRetry{fmt.Sprintf("tund could not reach %s: %v", displayHost(dl.Server), unwrapDial(err))}
	}
	switch {
	case status == http.StatusOK:
		var ok struct {
			Account string `json:"account"`
		}
		_ = json.Unmarshal(body, &ok)
		return &LoginResult{Server: dl.Server, Token: dl.token, Account: ok.Account}, nil
	case status == http.StatusForbidden:
		return nil, ErrLoginDenied
	case status == http.StatusGone, status == http.StatusNotFound:
		return nil, ErrLoginExpired
	case status == http.StatusBadRequest:
		return nil, &callbackRetry{"This approval is out of date, probably from an older tab."}
	case status >= 500:
		return nil, &callbackRetry{fmt.Sprintf("%s had a problem (%s).", displayHost(dl.Server), apiErrorMessage(body, status))}
	default:
		return nil, fmt.Errorf("login failed: %s", apiErrorMessage(body, status))
	}
}

// Login runs the login on the terminal. With a browser on this machine it
// opens the dashboard and waits for the redirect back to the CLI; otherwise
// (--no-browser, SSH, no display) it shows a code to approve on any device.
func Login(ctx context.Context, opts LoginOptions) (*LoginResult, error) {
	d := opts.Display
	if d == nil {
		d = NewDisplay(false)
	}
	browser := !opts.NoBrowser && d.pretty && browserAvailable()
	start := StartDeviceLogin
	if browser {
		start = StartBrowserLogin
	}
	dl, err := start(ctx, opts.Server, opts.HTTPClient)
	if err != nil {
		return nil, err
	}
	opened := browser && dl.OpenBrowser()
	label, expiry := "Waiting for approval", "code"
	if dl.Callback() {
		d.browserLoginPrompt(dl.Server, dl.VerificationURLComplete, opened)
		label, expiry = "Waiting for you to approve in the browser", "link"
	} else {
		d.loginPrompt(dl.Server, deviceCode{
			UserCode:                dl.UserCode,
			VerificationURL:         dl.VerificationURL,
			VerificationURLComplete: dl.VerificationURLComplete,
		}, opened)
	}
	sp := d.startSpinner(label, expiry, dl.ExpiresAt)
	defer sp.stop()
	return dl.Wait(ctx, sp.note)
}

func postJSON(ctx context.Context, hc *http.Client, u string, v any) (int, []byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tund/"+Version)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, body, err
}

func apiErrorMessage(body []byte, status int) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	if s := strings.TrimSpace(string(body)); s != "" && len(s) < 200 && !strings.HasPrefix(s, "<") {
		return s
	}
	return fmt.Sprintf("HTTP %d %s", status, http.StatusText(status))
}

// browserAvailable reports whether a browser can be opened on this machine:
// not in SSH sessions, and on Linux/BSD only with a display.
func browserAvailable() bool {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CLIENT") != "" {
		return false
	}
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

// openBrowser opens an http(s) URL in the default browser. It returns false
// where that cannot work (see browserAvailable).
func openBrowser(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || !browserAvailable() {
		return false
	}
	target := u.String()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	go func() { _ = cmd.Wait() }()
	return true
}
