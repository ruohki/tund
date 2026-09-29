package client

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"sync"
	"time"
)

// Loopback callback for `tund login` (RFC 8252 style). The CLI listens on
// 127.0.0.1 on a random port; after the user approves, the dashboard redirects
// the browser to /callback?state=…&code=…. The CLI checks the state, redeems
// the one-time code with the server (which only then activates the token) and
// sends the browser on to the dashboard's "logged in" page.

const callbackPath = "/callback"

type callbackResult struct {
	res *LoginResult
	err error
}

// callbackRetry is a failed redemption the user can retry by approving again
// in the dashboard tab; the login keeps waiting.
type callbackRetry struct{ msg string }

func (e *callbackRetry) Error() string { return e.msg }

type callback struct {
	ln     net.Listener
	srv    *http.Server
	state  string
	result chan callbackResult // gets the first result that ends the login
	once   sync.Once
}

func listenCallback() (*callback, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		ln.Close()
		return nil, err
	}
	return &callback{ln: ln, state: base64.RawURLEncoding.EncodeToString(b), result: make(chan callbackResult, 1)}, nil
}

func (cb *callback) port() int { return cb.ln.Addr().(*net.TCPAddr).Port }

func (cb *callback) start(dl *DeviceLogin, doneURL string) {
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, cb.handler(dl, doneURL))
	cb.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = cb.srv.Serve(cb.ln) }()
}

func (cb *callback) settle(res *LoginResult, err error) {
	cb.once.Do(func() { cb.result <- callbackResult{res, err} })
}

// close stops listening; a response in flight (the final redirect) still
// reaches the browser.
func (cb *callback) close() {
	if cb.srv == nil {
		cb.ln.Close()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = cb.srv.Shutdown(ctx)
}

func (cb *callback) handler(dl *DeviceLogin, doneURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		q := r.URL.Query()
		// The state keeps other web pages from settling (or failing) this
		// login by navigating to the port.
		if r.Method != http.MethodGet || q.Get("code") == "" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(cb.state)) != 1 {
			callbackPage(w, http.StatusBadRequest, "This link doesn't match your terminal",
				"It doesn't belong to the login that's waiting in your terminal. Run tund login again to start over.")
			return
		}
		res, err := dl.redeem(r.Context(), q.Get("code"))
		var retry *callbackRetry
		switch {
		case err == nil:
			http.Redirect(w, r, doneURL, http.StatusSeeOther)
			cb.settle(res, nil)
		case errors.As(err, &retry):
			callbackPage(w, http.StatusBadGateway, "Your terminal couldn't finish logging in",
				retry.msg+" Go back to the previous tab and approve again.")
		case errors.Is(err, ErrLoginDenied):
			callbackPage(w, http.StatusForbidden, "Login denied", "The terminal was not logged in. You can close this tab.")
			cb.settle(nil, err)
		case errors.Is(err, ErrLoginExpired):
			callbackPage(w, http.StatusGone, "This login expired", "Run tund login in your terminal to start again.")
			cb.settle(nil, err)
		default:
			callbackPage(w, http.StatusBadGateway, "Login failed", err.Error())
			cb.settle(nil, err)
		}
	}
}

// callbackPage is the CLI's own page for callbacks that can't go on to the
// dashboard.
func callbackPage(w http.ResponseWriter, status int, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>tund login</title>
<style>
:root{color-scheme:light dark;--bg:#fafaf9;--ink:#1c1917;--muted:#57534e;--line:#e7e5e4}
@media (prefers-color-scheme:dark){:root{--bg:#0c0a09;--ink:#f5f5f4;--muted:#a8a29e;--line:#292524}}
body{margin:0;background:var(--bg);color:var(--ink);font:15px/1.55 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:34rem;margin:12vh auto;padding:0 16px}
p.brand{font:600 13px ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--muted);border-bottom:1px solid var(--line);padding-bottom:12px;margin:0 0 24px}
h1{font-size:24px;line-height:1.25;letter-spacing:-.01em;margin:0 0 8px}
p{color:var(--muted);margin:0}
</style></head>
<body><main><p class="brand">tund login</p><h1>%s</h1><p>%s</p></main></body></html>
`, html.EscapeString(title), html.EscapeString(msg))
}
