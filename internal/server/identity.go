package server

import (
	"context"
	"net/http"
	"strings"
)

// identity is who passed a tunnel's access policy. It travels in the signed
// visitor cookie and is forwarded to the local app as X-Tund-* headers.
type identity struct {
	Method        string   `json:"m,omitempty"`  // "oidc" or "password"
	Email         string   `json:"e,omitempty"`  // email claim
	EmailVerified string   `json:"ev,omitempty"` // "true"/"false" when the IdP sends it
	Name          string   `json:"nm,omitempty"` // name claim
	Username      string   `json:"u,omitempty"`  // preferred_username (or nickname)
	Sub           string   `json:"s,omitempty"`  // subject
	Groups        []string `json:"g,omitempty"`  // groups claim
	Issuer        string   `json:"i,omitempty"`
}

const (
	maxGroups     = 40
	maxGroupBytes = 1500
)

// capGroups keeps the cookie small: at most maxGroups names and maxGroupBytes.
func capGroups(in []string) []string {
	var out []string
	size := 0
	for _, g := range in {
		g = strings.ReplaceAll(cleanHeaderValue(g), ",", "")
		if g == "" {
			continue
		}
		if len(out) >= maxGroups || size+len(g) > maxGroupBytes {
			break
		}
		out = append(out, g)
		size += len(g)
	}
	return out
}

// cleanHeaderValue drops control characters so a claim can't inject headers.
func cleanHeaderValue(v string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(v))
}

type identityKey struct{}

func withIdentity(r *http.Request, id *identity) *http.Request {
	if id == nil {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), identityKey{}, id))
}

func identityFrom(ctx context.Context) *identity {
	id, _ := ctx.Value(identityKey{}).(*identity)
	return id
}

// stripTundHeaders removes every X-Tund-* header, so visitors can never
// spoof the identity headers the edge sets.
func stripTundHeaders(h http.Header) {
	for k := range h {
		if len(k) >= 7 && strings.EqualFold(k[:7], "X-Tund-") {
			delete(h, k)
		}
	}
}

// identityFromHeaders recovers the identity recorded with a captured request,
// so replays reach the app as the same signed-in visitor.
func identityFromHeaders(h http.Header) *identity {
	if h.Get("X-Tund-Auth") == "" {
		return nil
	}
	id := &identity{
		Method:        h.Get("X-Tund-Auth"),
		Email:         h.Get("X-Tund-User-Email"),
		EmailVerified: h.Get("X-Tund-User-Email-Verified"),
		Name:          h.Get("X-Tund-User-Name"),
		Username:      h.Get("X-Tund-User-Username"),
		Sub:           h.Get("X-Tund-User-Id"),
		Issuer:        h.Get("X-Tund-Idp"),
	}
	if g := h.Get("X-Tund-User-Groups"); g != "" {
		id.Groups = strings.Split(g, ",")
	}
	return id
}

// setIdentityHeaders writes the X-Tund-* identity headers (see docs/SPEC.md).
func setIdentityHeaders(h http.Header, id *identity) {
	if id == nil || id.Method == "" {
		return
	}
	set := func(k, v string) {
		if v = cleanHeaderValue(v); v != "" {
			h.Set(k, v)
		}
	}
	set("X-Tund-Auth", id.Method)
	set("X-Tund-User-Email", id.Email)
	set("X-Tund-User-Email-Verified", id.EmailVerified)
	set("X-Tund-User-Name", id.Name)
	set("X-Tund-User-Username", id.Username)
	set("X-Tund-User-Id", id.Sub)
	set("X-Tund-User-Groups", strings.Join(id.Groups, ","))
	set("X-Tund-Idp", id.Issuer)
}
