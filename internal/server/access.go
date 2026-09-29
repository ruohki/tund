package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"tund/internal/protocol"
)

const (
	authCookieName  = "_tund_auth"
	oidcCookiePfx   = "_tund_oidc_"
	authCookieTTL   = 7 * 24 * time.Hour
	oidcStateTTL    = 10 * time.Minute
	handoffTokenTTL = 2 * time.Minute
)

// signer produces compact HMAC-signed JSON tokens: base64(payload).base64(mac).
type signer struct{ key []byte }

func (s signer) mac(purpose string, payload []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(purpose + "\x00"))
	m.Write(payload)
	return m.Sum(nil)
}

func (s signer) Sign(purpose string, v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(s.mac(purpose, b))
}

func (s signer) Verify(purpose, token string, v any) error {
	p, m, ok := strings.Cut(token, ".")
	if !ok {
		return errors.New("malformed token")
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	sig, err2 := base64.RawURLEncoding.DecodeString(m)
	if err1 != nil || err2 != nil || !hmac.Equal(sig, s.mac(purpose, payload)) {
		return errors.New("invalid token signature")
	}
	return json.Unmarshal(payload, v)
}

// authClaims is stored in the visitor cookie on a tunnel host.
type authClaims struct {
	Host        string `json:"h"`
	Fingerprint string `json:"f"`
	Expires     int64  `json:"x"`
	identity
}

// oidcRequest travels from the tunnel host to /_tund/oidc/start.
type oidcRequest struct {
	Host    string `json:"h"`
	Next    string `json:"n"`
	Expires int64  `json:"x"`
}

// oidcState is kept in a cookie on the dashboard host during the login.
type oidcState struct {
	Host     string `json:"h"`
	Next     string `json:"n"`
	Provider string `json:"p"`
	Verifier string `json:"v"`
	Nonce    string `json:"o"`
	Expires  int64  `json:"x"`
}

// handoff carries a successful login back to the tunnel host.
type handoff struct {
	Host        string `json:"h"`
	Fingerprint string `json:"f"`
	identity
	Next    string `json:"n"`
	JTI     string `json:"j"`
	Expires int64  `json:"x"`
}

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) secureCookies() bool { return s.cfg.PublicScheme == "https" }

func (s *Server) setAuthCookie(w http.ResponseWriter, t *Tunnel, id identity) {
	pol := t.Policy()
	exp := time.Now().Add(authCookieTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    s.signer.Sign("auth", authClaims{Host: t.Hostname, Fingerprint: pol.Fingerprint(), Expires: exp.Unix(), identity: id}),
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) visitorAuthorized(r *http.Request, t *Tunnel, pol Policy) (*identity, bool) {
	// Other tunnels share the parent domain and can plant cookies with the
	// same name ("cookie tossing"), so check every candidate, not just the first.
	for _, c := range r.CookiesNamed(authCookieName) {
		var cl authClaims
		if err := s.signer.Verify("auth", c.Value, &cl); err != nil {
			continue
		}
		if cl.Host == t.Hostname && cl.Fingerprint == pol.Fingerprint() && time.Now().Unix() < cl.Expires &&
			(pol.Mode != protocol.AuthOIDC || pol.Allowed(cl.Email, cl.Groups)) {
			id := cl.identity
			if id.Method == "" {
				id.Method = pol.Mode
			}
			return &id, true
		}
	}
	return nil, false
}

func wantsHTML(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.Contains(r.Header.Get("Accept"), "text/html")
}

// safeNext keeps redirects on the same host.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

// checkAccess enforces the tunnel's policy. It returns ok=false after writing
// a response when the visitor is not allowed through; otherwise the identity
// of a visitor who passed a policy (nil for public tunnels).
func (s *Server) checkAccess(w http.ResponseWriter, r *http.Request, t *Tunnel) (*identity, bool) {
	pol := t.Policy()
	switch pol.Mode {
	case protocol.AuthNone, "":
		return nil, true
	case protocol.AuthPassword:
		if id, ok := s.visitorAuthorized(r, t, pol); ok {
			return id, true
		}
		if _, pw, ok := r.BasicAuth(); ok && t.checkPassword(pw) {
			// The credential was meant for the edge, not for the local app.
			r.Header.Del("Authorization")
			return &identity{Method: protocol.AuthPassword}, true
		}
		if wantsHTML(r) {
			s.renderPage(w, r, http.StatusUnauthorized, pageLogin, map[string]any{"Host": t.Hostname, "Next": r.URL.RequestURI()})
			return nil, false
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="`+t.Hostname+`", charset="UTF-8"`)
		writeJSONError(w, http.StatusUnauthorized, "this tunnel is password protected; use HTTP basic auth with any username")
		return nil, false
	case protocol.AuthOIDC:
		if id, ok := s.visitorAuthorized(r, t, pol); ok {
			return id, true
		}
		if pol.ProviderID == "" {
			s.renderPage(w, r, http.StatusServiceUnavailable, pageMessage, map[string]any{
				"Title": "Login unavailable", "Message": "This site is protected by a single sign-on provider that no longer exists. The owner needs to update its access settings.",
			})
			return nil, false
		}
		if wantsHTML(r) {
			req := s.signer.Sign("oidc-req", oidcRequest{Host: t.Hostname, Next: r.URL.RequestURI(), Expires: time.Now().Add(oidcStateTTL).Unix()})
			http.Redirect(w, r, s.cfg.DashboardURL()+"/_tund/oidc/start?r="+url.QueryEscape(req), http.StatusFound)
			return nil, false
		}
		writeJSONError(w, http.StatusUnauthorized, "this tunnel requires single sign-on; open it in a browser")
		return nil, false
	}
	writeJSONError(w, http.StatusForbidden, "access denied")
	return nil, false
}

// loginLimiter slows down password guessing per visitor IP and host.
type loginLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	var recent []time.Time
	for _, t := range l.hits[key] {
		if now.Sub(t) < 5*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 10 {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	if len(l.hits) > 10000 {
		l.hits = map[string][]time.Time{}
	}
	return true
}

// serveTunnelAuth handles /_tund/auth/* on tunnel hosts. It returns false when
// the path is not one of ours.
func (s *Server) serveTunnelAuth(w http.ResponseWriter, r *http.Request, t *Tunnel) bool {
	switch r.URL.Path {
	case "/_tund/auth/login":
		pol := t.Policy()
		next := safeNext(r.FormValue("next"))
		if pol.Mode != protocol.AuthPassword {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return true
		}
		if r.Method != http.MethodPost {
			s.renderPage(w, r, http.StatusOK, pageLogin, map[string]any{"Host": t.Hostname, "Next": next})
			return true
		}
		key := clientIP(r) + "|" + t.Hostname
		if !s.limiter.allow(key) {
			s.renderPage(w, r, http.StatusTooManyRequests, pageLogin, map[string]any{"Host": t.Hostname, "Next": next, "Error": "Too many attempts. Wait a few minutes and try again."})
			return true
		}
		if !t.checkPassword(r.PostFormValue("password")) {
			time.Sleep(400 * time.Millisecond)
			s.renderPage(w, r, http.StatusUnauthorized, pageLogin, map[string]any{"Host": t.Hostname, "Next": next, "Error": "That password is not correct."})
			return true
		}
		s.setAuthCookie(w, t, identity{Method: protocol.AuthPassword})
		http.Redirect(w, r, next, http.StatusSeeOther)
		return true

	case "/_tund/auth/complete":
		var h handoff
		err := s.signer.Verify("handoff", r.URL.Query().Get("t"), &h)
		switch {
		case err != nil, h.Host != t.Hostname, time.Now().Unix() > h.Expires:
			s.renderPage(w, r, http.StatusBadRequest, pageMessage, map[string]any{"Title": "Login link expired", "Message": "This sign-in link is invalid or expired.", "Retry": "/"})
			return true
		case h.Fingerprint != t.Policy().Fingerprint():
			s.renderPage(w, r, http.StatusConflict, pageMessage, map[string]any{"Title": "Access settings changed", "Message": "The access settings of this site changed while you were signing in.", "Retry": safeNext(h.Next)})
			return true
		case !s.useJTI(h.JTI, time.Unix(h.Expires, 0)):
			s.renderPage(w, r, http.StatusBadRequest, pageMessage, map[string]any{"Title": "Login link already used", "Message": "This sign-in link was already used.", "Retry": safeNext(h.Next)})
			return true
		}
		s.setAuthCookie(w, t, h.identity)
		http.Redirect(w, r, safeNext(h.Next), http.StatusSeeOther)
		return true

	case "/_tund/warning/accept":
		s.acceptWarning(w, r, t)
		return true

	case "/_tund/auth/logout":
		http.SetCookie(w, &http.Cookie{Name: authCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
		s.renderPage(w, r, http.StatusOK, pageMessage, map[string]any{"Title": "Signed out", "Message": "You have been signed out of " + t.Hostname + ".", "Retry": "/"})
		return true
	}
	return false
}

// useJTI marks a one-time token id as used; false if it was used before.
func (s *Server) useJTI(jti string, exp time.Time) bool {
	if jti == "" {
		return false
	}
	now := time.Now()
	s.jtiMu.Lock()
	defer s.jtiMu.Unlock()
	for k, e := range s.jtis {
		if now.After(e) {
			delete(s.jtis, k)
		}
	}
	if _, used := s.jtis[jti]; used {
		return false
	}
	s.jtis[jti] = exp
	return true
}

// stringList reads a claim that should be a list of strings (a lone string
// is accepted too); other shapes are ignored.
func stringList(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}
	}
	return nil
}

// --- OIDC (served on the dashboard host) ---

type oidcDiscovery struct {
	provider *oidc.Provider
	fetched  time.Time
}

func (s *Server) discover(ctx context.Context, issuer string) (*oidc.Provider, error) {
	s.oidcMu.Lock()
	d, ok := s.oidcCache[issuer]
	s.oidcMu.Unlock()
	if ok && time.Since(d.fetched) < time.Hour {
		return d.provider, nil
	}
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	s.oidcMu.Lock()
	s.oidcCache[issuer] = oidcDiscovery{provider: p, fetched: time.Now()}
	s.oidcMu.Unlock()
	return p, nil
}

func (s *Server) oauthConfig(ctx context.Context, p *OIDCProvider) (*oauth2.Config, *oidc.Provider, error) {
	op, err := s.discover(ctx, p.Issuer)
	if err != nil {
		return nil, nil, fmt.Errorf("could not reach the identity provider %s: %w", p.Issuer, err)
	}
	scopes := strings.Fields(strings.ReplaceAll(p.Scopes, ",", " "))
	hasOpenID := false
	for _, sc := range scopes {
		hasOpenID = hasOpenID || sc == oidc.ScopeOpenID
	}
	if !hasOpenID {
		scopes = append([]string{oidc.ScopeOpenID}, scopes...)
	}
	return &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		Endpoint:     op.Endpoint(),
		RedirectURL:  s.cfg.DashboardURL() + "/_tund/oidc/callback",
		Scopes:       scopes,
	}, op, nil
}

func (s *Server) oidcError(w http.ResponseWriter, r *http.Request, status int, title, msg string) {
	s.renderPage(w, r, status, pageMessage, map[string]any{"Title": title, "Message": msg})
}

func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	var req oidcRequest
	if err := s.signer.Verify("oidc-req", r.URL.Query().Get("r"), &req); err != nil || time.Now().Unix() > req.Expires {
		s.oidcError(w, r, http.StatusBadRequest, "Login link expired", "Go back to the site you were visiting and try again.")
		return
	}
	t := s.reg.Lookup(req.Host)
	if t == nil {
		s.oidcError(w, r, http.StatusNotFound, "Tunnel offline", req.Host+" is not online right now.")
		return
	}
	pol := t.Policy()
	if pol.Mode != protocol.AuthOIDC || pol.ProviderID == "" {
		http.Redirect(w, r, t.PublicURL+safeNext(req.Next), http.StatusFound)
		return
	}
	p, err := s.store.OIDCProvider(r.Context(), pol.ProviderID)
	if err != nil {
		s.oidcError(w, r, http.StatusServiceUnavailable, "Login unavailable", "The identity provider configured for this site could not be loaded.")
		return
	}
	oc, _, err := s.oauthConfig(r.Context(), p)
	if err != nil {
		s.oidcError(w, r, http.StatusBadGateway, "Identity provider unreachable", err.Error())
		return
	}
	st := oidcState{
		Host: req.Host, Next: req.Next, Provider: p.ID,
		Verifier: oauth2.GenerateVerifier(), Nonce: randomToken(16),
		Expires: time.Now().Add(oidcStateTTL).Unix(),
	}
	sid := randomToken(12)
	http.SetCookie(w, &http.Cookie{
		Name:     oidcCookiePfx + sid,
		Value:    s.signer.Sign("oidc-state", st),
		Path:     "/_tund/oidc/",
		MaxAge:   int(oidcStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, oc.AuthCodeURL(sid, oauth2.S256ChallengeOption(st.Verifier), oidc.Nonce(st.Nonce)), http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sid := q.Get("state")
	c, err := r.Cookie(oidcCookiePfx + sid)
	if sid == "" || err != nil {
		s.oidcError(w, r, http.StatusBadRequest, "Login session expired", "Your sign-in took too long or cookies are blocked. Go back to the site and try again.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: c.Name, Value: "", Path: "/_tund/oidc/", MaxAge: -1, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
	var st oidcState
	if err := s.signer.Verify("oidc-state", c.Value, &st); err != nil || time.Now().Unix() > st.Expires {
		s.oidcError(w, r, http.StatusBadRequest, "Login session expired", "Go back to the site and try again.")
		return
	}
	if e := q.Get("error"); e != "" {
		msg := e
		if d := q.Get("error_description"); d != "" {
			msg += ": " + d
		}
		s.oidcError(w, r, http.StatusForbidden, "Sign-in was not completed", msg)
		return
	}
	t := s.reg.Lookup(st.Host)
	if t == nil {
		s.oidcError(w, r, http.StatusNotFound, "Tunnel offline", st.Host+" went offline during sign-in.")
		return
	}
	pol := t.Policy()
	if pol.Mode != protocol.AuthOIDC || pol.ProviderID != st.Provider {
		s.oidcError(w, r, http.StatusConflict, "Access settings changed", "The access settings of "+st.Host+" changed during sign-in. Try again.")
		return
	}
	p, err := s.store.OIDCProvider(r.Context(), st.Provider)
	if err != nil {
		s.oidcError(w, r, http.StatusServiceUnavailable, "Login unavailable", "The identity provider for this site could not be loaded.")
		return
	}
	oc, op, err := s.oauthConfig(r.Context(), p)
	if err != nil {
		s.oidcError(w, r, http.StatusBadGateway, "Identity provider unreachable", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tok, err := oc.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		s.oidcError(w, r, http.StatusBadGateway, "Sign-in failed", "Exchanging the authorization code failed: "+err.Error())
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		s.oidcError(w, r, http.StatusBadGateway, "Sign-in failed", "The identity provider did not return an ID token. Make sure the client is an OpenID Connect client and the openid scope is allowed.")
		return
	}
	idt, err := op.Verifier(&oidc.Config{ClientID: p.ClientID}).Verify(ctx, raw)
	if err != nil {
		s.oidcError(w, r, http.StatusBadGateway, "Sign-in failed", "The ID token could not be verified: "+err.Error())
		return
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(st.Nonce)) != 1 {
		s.oidcError(w, r, http.StatusBadRequest, "Sign-in failed", "The ID token nonce does not match. Try again.")
		return
	}
	var claims struct {
		Email         string          `json:"email"`
		EmailVerified *bool           `json:"email_verified"`
		Name          string          `json:"name"`
		Username      string          `json:"preferred_username"`
		Nickname      string          `json:"nickname"`
		Groups        json.RawMessage `json:"groups"`
	}
	idt.Claims(&claims)
	if claims.Email == "" || claims.Name == "" || len(claims.Groups) == 0 {
		// Some IdPs only put profile claims into the userinfo response.
		if ui, err := op.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
			var extra struct {
				Name     string          `json:"name"`
				Username string          `json:"preferred_username"`
				Groups   json.RawMessage `json:"groups"`
			}
			ui.Claims(&extra)
			if claims.Email == "" {
				claims.Email = ui.Email
				if !ui.EmailVerified {
					f := false
					claims.EmailVerified = &f
				}
			}
			if claims.Name == "" {
				claims.Name = extra.Name
			}
			if claims.Username == "" {
				claims.Username = extra.Username
			}
			if len(claims.Groups) == 0 {
				claims.Groups = extra.Groups
			}
		}
	}
	if claims.EmailVerified != nil && !*claims.EmailVerified && len(pol.Allow) > 0 {
		s.oidcError(w, r, http.StatusForbidden, "Email not verified", "Your email address "+claims.Email+" is not verified by the identity provider.")
		return
	}
	id := identity{
		Method:   protocol.AuthOIDC,
		Email:    claims.Email,
		Name:     claims.Name,
		Username: claims.Username,
		Sub:      idt.Subject,
		Groups:   capGroups(stringList(claims.Groups)),
		Issuer:   idt.Issuer,
	}
	if id.Username == "" {
		id.Username = claims.Nickname
	}
	if claims.EmailVerified != nil {
		id.EmailVerified = strconv.FormatBool(*claims.EmailVerified)
	}
	if !pol.Allowed(id.Email, id.Groups) {
		who := claims.Email
		if who == "" {
			who = "Your account"
		}
		s.renderPage(w, r, http.StatusForbidden, pageMessage, map[string]any{
			"Title": "Access denied", "Message": who + " is not allowed to access " + st.Host + ". Ask the owner to add you to the allow list, or sign in with another account.",
			"Retry": t.PublicURL + safeNext(st.Next),
		})
		return
	}
	h := handoff{Host: st.Host, Fingerprint: pol.Fingerprint(), Next: st.Next, JTI: randomToken(12), Expires: time.Now().Add(handoffTokenTTL).Unix(), identity: id}
	http.Redirect(w, r, t.PublicURL+"/_tund/auth/complete?t="+url.QueryEscape(s.signer.Sign("handoff", h)), http.StatusFound)
}
