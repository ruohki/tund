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
}

// DeviceLogin is a started device authorization: show UserCode and
// VerificationURL(Complete) to the user, then call Poll.
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
}

// StartDeviceLogin creates a token locally and registers its hash with the
// server, which answers with the code the user has to approve.
func StartDeviceLogin(ctx context.Context, server string, hc *http.Client) (*DeviceLogin, error) {
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

	status, body, err := postJSON(ctx, hc, codeURL, map[string]string{
		"token_hash":      TokenHash(token),
		"token_prefix":    TokenPrefix(token),
		"client_hostname": hostname,
		"client_os":       runtime.GOOS + "/" + runtime.GOARCH,
	})
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
	return &DeviceLogin{
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
	}, nil
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

// Login runs the device authorization flow on the terminal: it shows the
// code, opens the browser when possible and waits for approval.
func Login(ctx context.Context, opts LoginOptions) (*LoginResult, error) {
	d := opts.Display
	if d == nil {
		d = NewDisplay(false)
	}
	dl, err := StartDeviceLogin(ctx, opts.Server, opts.HTTPClient)
	if err != nil {
		return nil, err
	}
	opened := false
	if !opts.NoBrowser && d.pretty {
		opened = dl.OpenBrowser()
	}
	d.loginPrompt(dl.Server, deviceCode{
		UserCode:                dl.UserCode,
		VerificationURL:         dl.VerificationURL,
		VerificationURLComplete: dl.VerificationURLComplete,
	}, opened)
	sp := d.startSpinner("Waiting for approval", dl.ExpiresAt)
	defer sp.stop()
	return dl.Poll(ctx, sp.note)
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

// openBrowser opens an http(s) URL in the default browser. It returns false
// where that cannot work (SSH sessions, Linux without a display).
func openBrowser(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return false
	}
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CLIENT") != "" {
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
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return false
	}
	go func() { _ = cmd.Wait() }()
	return true
}
