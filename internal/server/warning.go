package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tund/internal/protocol"
)

// Browser warning page: an anti-phishing interstitial shown to people who
// open a tunnel on the shared base domain in a browser (see docs/SPEC.md).

const (
	warnCookieName = "_tund_warned"
	warnCookieTTL  = 7 * 24 * time.Hour
)

type warnClaims struct {
	Host    string `json:"h"`
	Expires int64  `json:"x"`
}

// isBrowserNavigation matches top-level page loads by a browser, but not
// fetch/XHR, webhooks, curl or API clients.
func isBrowserNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/html") || !strings.Contains(r.Header.Get("User-Agent"), "Mozilla/") {
		return false
	}
	// Sec-Fetch-Dest is sent by modern browsers; only documents get the page.
	if d := r.Header.Get("Sec-Fetch-Dest"); d != "" && d != "document" && d != "iframe" {
		return false
	}
	return true
}

func (s *Server) needsWarning(r *http.Request, t *Tunnel) bool {
	if !t.warn.Load() || t.Policy().Mode != protocol.AuthNone {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/_tund/") || r.Header.Get(protocol.HeaderSkipWarning) != "" || !isBrowserNavigation(r) {
		return false
	}
	// Other tunnels can plant cookies on the parent domain, so every candidate
	// is checked and must be signed for this exact host.
	for _, c := range r.CookiesNamed(warnCookieName) {
		var cl warnClaims
		if s.signer.Verify("warned", c.Value, &cl) == nil && cl.Host == t.Hostname && time.Now().Unix() < cl.Expires {
			return false
		}
	}
	return true
}

func (s *Server) renderWarning(w http.ResponseWriter, r *http.Request, t *Tunnel) {
	w.Header().Set("X-Tund-Warning", "1")
	w.Header().Set("X-Robots-Tag", "noindex")
	s.renderPage(w, r, http.StatusOK, pageWarning, map[string]any{
		"Host":     t.Hostname,
		"Next":     r.URL.RequestURI(),
		"Service":  s.cfg.DashboardHost,
		"Abuse":    s.rt().AbuseContact,
		"AbuseURL": s.cfg.DashboardURL() + "/report?host=" + url.QueryEscape(t.Hostname),
	})
}

func abuseURL(contact, host string) string {
	switch {
	case contact == "":
		return ""
	case strings.Contains(contact, "://"):
		return contact
	case strings.Contains(contact, "@"):
		return "mailto:" + contact + "?subject=Abuse%20report%3A%20" + host
	}
	return ""
}

func (s *Server) acceptWarning(w http.ResponseWriter, r *http.Request, t *Tunnel) {
	next := safeNext(r.FormValue("next"))
	if r.Method != http.MethodPost {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	exp := time.Now().Add(warnCookieTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     warnCookieName,
		Value:    s.signer.Sign("warned", warnClaims{Host: t.Hostname, Expires: exp.Unix()}),
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// refreshWarning re-evaluates the warning for a user's live tunnels after an
// admin changed the account's trusted flag.
func (s *Server) refreshWarning(userID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	trusted, err := s.store.UserTrusted(ctx, userID)
	if err != nil {
		logf("refresh warning for %s: %v", userID, err)
		return
	}
	for _, t := range s.reg.Tunnels() {
		if t.UserID == userID {
			t.ownerTrusted.Store(trusted)
			s.updateWarn(t)
		}
	}
}

// updateWarn recomputes whether browser visitors of t see the warning page.
func (s *Server) updateWarn(t *Tunnel) {
	rt := s.rt()
	t.warn.Store(t.Proto == protocol.ProtoHTTP && rt.BrowserWarning && !t.ownerTrusted.Load() &&
		(s.certs.underBase(t.Hostname) || rt.WarnCustomDomains))
}
