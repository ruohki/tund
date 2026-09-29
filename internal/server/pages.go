package server

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

const (
	pageNotFound      = "notfound"
	pageOffline       = "offline"
	pageBadGateway    = "badgateway"
	pageLogin         = "login"
	pageMessage       = "message"
	pageDashboardDown = "dashboarddown"
	pageWarning       = "warning"
)

const pageBase = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex">
<title>{{template "title" .}}</title>
<style>
:root{--bg:#f6f7f9;--card:#fff;--fg:#0d1117;--muted:#5b6472;--line:#e3e6eb;--accent:#0f766e;--accent-fg:#fff;--code:#eef1f4;--danger:#b42318}
@media (prefers-color-scheme:dark){:root{--bg:#0b0d10;--card:#12151a;--fg:#e8eaed;--muted:#98a1ad;--line:#232830;--accent:#2dd4bf;--accent-fg:#04201d;--code:#1a1f26;--danger:#f97066}}
*{box-sizing:border-box}html,body{margin:0;background:var(--bg);color:var(--fg)}
body{font:15px/1.55 ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;min-height:100vh;display:grid;place-items:center;padding:24px 16px}
main{width:100%;max-width:480px;background:var(--card);border:1px solid var(--line);border-radius:14px;padding:32px}
.brand{display:flex;align-items:center;gap:8px;color:var(--muted);font:600 12px/1 ui-monospace,SFMono-Regular,Menlo,monospace;letter-spacing:.08em;text-transform:uppercase;margin-bottom:24px}
.brand i{width:8px;height:8px;border-radius:2px;background:var(--accent);display:inline-block}
.code{font:700 13px ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--muted)}
h1{font-size:22px;line-height:1.25;margin:6px 0 10px;letter-spacing:-.01em}
p{margin:0 0 12px;color:var(--muted)}
code,pre{font:13px ui-monospace,SFMono-Regular,Menlo,monospace;background:var(--code);border-radius:6px}
code{padding:2px 6px;color:var(--fg);word-break:break-all}
pre{padding:12px 14px;overflow:auto;color:var(--fg);margin:14px 0 0}
.detail{font:12.5px ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--danger);background:var(--code);border-radius:6px;padding:10px 12px;margin-top:12px;word-break:break-word}
form{margin-top:20px;display:grid;gap:10px}
input{font:inherit;padding:11px 12px;border-radius:8px;border:1px solid var(--line);background:var(--bg);color:var(--fg);width:100%}
input:focus{outline:2px solid var(--accent);outline-offset:1px}
button,.btn{font-family:inherit;font-size:14px;font-weight:600;line-height:1;height:40px;padding:0 16px;border:1px solid transparent;border-radius:8px;background:var(--accent);color:var(--accent-fg);cursor:pointer;text-decoration:none;display:inline-flex;align-items:center;justify-content:center;box-sizing:border-box}
.err{color:var(--danger);font-size:14px;margin:0}
ul{margin:10px 0 0;padding-left:18px;color:var(--muted)}li{margin:4px 0}
.foot{margin-top:24px;padding-top:16px;border-top:1px solid var(--line);font-size:12.5px;color:var(--muted)}
.host{font:600 17px/1.35 ui-monospace,SFMono-Regular,Menlo,monospace;word-break:break-all;background:var(--code);border-radius:8px;padding:12px 14px;margin:4px 0 16px;color:var(--fg)}
.warnbox{border-left:3px solid #d97706;background:var(--code);border-radius:6px;padding:10px 12px;margin:14px 0 4px;color:var(--fg);font-size:14px}
.row{display:flex;gap:10px;flex-wrap:wrap;align-items:center;margin-top:20px}
.row form{margin:0;display:block}
.ghost{background:transparent;color:var(--fg);border-color:var(--line)}
a{color:inherit}
</style></head>
<body><main>
<div class="brand"><i></i>tund</div>
{{template "body" .}}
</main></body></html>`

var pageBodies = map[string]string{
	pageNotFound: `{{define "title"}}No tunnel here{{end}}{{define "body"}}
<div class="code">404</div>
<h1>There is no tunnel at this address</h1>
<p><code>{{.Host}}</code> is not connected to any running tunnel.</p>
<p>If this is your address, start a tunnel for it:</p>
<pre>tund http 3000{{if .Label}} --subdomain {{.Label}}{{end}}</pre>
<div class="foot">Check the spelling of the URL, or ask the person who shared it whether their tunnel is still running.</div>
{{end}}`,

	pageOffline: `{{define "title"}}Tunnel offline{{end}}{{define "body"}}
<div class="code">404 · offline</div>
<h1>This tunnel is offline</h1>
<p><code>{{.Host}}</code> was last online <strong>{{.LastSeen}}</strong>.</p>
<p>The tund client that served it has disconnected. It will be reachable again as soon as the owner restarts it:</p>
<pre>tund http 3000{{if .Label}} --subdomain {{.Label}}{{end}}</pre>
{{end}}`,

	pageBadGateway: `{{define "title"}}Local service unavailable{{end}}{{define "body"}}
<div class="code">502 · bad gateway</div>
<h1>The tunnel is up, but the app behind it did not answer</h1>
<p>The tund client for <code>{{.Host}}</code> is connected, but it could not reach <code>{{.Local}}</code>.</p>
{{if .Detail}}<div class="detail">{{.Detail}}</div>{{end}}
<ul><li>Is your app running and listening on that port?</li><li>Does it listen on <code>localhost</code> / <code>127.0.0.1</code> (not only on another interface)?</li><li>If it serves HTTPS, start the tunnel with an <code>https://</code> address.</li></ul>
{{end}}`,

	pageLogin: `{{define "title"}}Password required · {{.Host}}{{end}}{{define "body"}}
<div class="code">protected</div>
<h1>Enter the password</h1>
<p><code>{{.Host}}</code> is protected. Ask the owner for the password.</p>
<form method="post" action="/_tund/auth/login">
<input type="hidden" name="next" value="{{.Next}}">
<input type="password" name="password" placeholder="Password" autocomplete="current-password" autofocus required>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<button type="submit">Continue</button>
</form>
<div class="foot">Scripts can send the password with HTTP basic auth, e.g. <code>curl -u :password …</code></div>
{{end}}`,

	pageMessage: `{{define "title"}}{{.Title}}{{end}}{{define "body"}}
<h1>{{.Title}}</h1>
<p>{{.Message}}</p>
{{if .Retry}}<p style="margin-top:20px"><a class="btn" href="{{.Retry}}">Continue</a></p>{{end}}
{{end}}`,

	pageWarning: `{{define "title"}}You are about to visit {{.Host}}{{end}}{{define "body"}}
<div class="code">heads up</div>
<h1>You are about to visit</h1>
<div class="host">{{.Host}}</div>
<p>This site is served through <strong>tund</strong>, a service that lets developers share apps running on their own computers. It is run by whoever sent you the link, <strong>not</strong> by tund.</p>
<div class="warnbox">Only continue if you trust the person who shared this link. Don't enter passwords, payment details or other personal information unless you are sure the site is legitimate.</div>
<div class="row">
<form method="post" action="/_tund/warning/accept"><input type="hidden" name="next" value="{{.Next}}"><button type="submit">Visit site</button></form>
<a class="btn ghost" href="about:blank" onclick="if(history.length>1){history.back();return false}">Go back</a>
</div>
<div class="foot">
{{if .AbuseURL}}Suspicious? <a href="{{.AbuseURL}}">Report abuse</a>.<br>{{end}}
Developers: this page appears once per browser. API calls, webhooks and requests with the header <code>Tund-Skip-Browser-Warning: 1</code> are never interrupted.
</div>
{{end}}`,

	pageDashboardDown: `{{define "title"}}Dashboard starting{{end}}{{define "body"}}
<div class="code">502</div>
<h1>The dashboard is not reachable right now</h1>
<p>The tund edge is running, but the management app did not respond. It may still be starting. Reload in a few seconds.</p>
{{if .Detail}}<div class="detail">{{.Detail}}</div>{{end}}
{{end}}`,
}

var pageTemplates = func() map[string]*template.Template {
	m := map[string]*template.Template{}
	for name, body := range pageBodies {
		m[name] = template.Must(template.Must(template.New("base").Parse(pageBase)).Parse(body))
	}
	return m
}()

// plainSummary renders a one-line version of a page for non-browser clients.
func plainSummary(page string, data map[string]any) string {
	get := func(k string) string { v, _ := data[k].(string); return v }
	switch page {
	case pageNotFound:
		return fmt.Sprintf("tund: no tunnel is running at %s", get("Host"))
	case pageOffline:
		return fmt.Sprintf("tund: the tunnel at %s is offline (last online %s)", get("Host"), get("LastSeen"))
	case pageBadGateway:
		return fmt.Sprintf("tund: the tunnel at %s is online but %s did not answer: %s", get("Host"), get("Local"), get("Detail"))
	case pageLogin:
		return "tund: password required"
	case pageDashboardDown:
		return "tund: dashboard unavailable"
	case pageWarning:
		return "tund: browser warning page for " + get("Host")
	}
	return "tund: " + get("Title") + ": " + get("Message")
}

func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Tund-Error", page)
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		fmt.Fprintln(w, plainSummary(page, data))
		return
	}
	var buf bytes.Buffer
	if err := pageTemplates[page].Execute(&buf, data); err != nil {
		logf("render %s: %v", page, err)
		http.Error(w, plainSummary(page, data), status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}
