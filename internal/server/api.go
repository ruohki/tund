package server

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Public, token-authenticated API under /_tund/api/v1/ (see docs/SPEC.md).

const apiPrefix = "/_tund/api/v1"

type accountKey struct{}

func (s *Server) newAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+apiPrefix+"/me", s.apiMe)
	mux.HandleFunc("GET "+apiPrefix+"/tunnels", s.apiTunnels)
	mux.HandleFunc("POST "+apiPrefix+"/tunnels/{id}/stop", s.apiStopTunnel)
	mux.HandleFunc("GET "+apiPrefix+"/connections", s.apiConnections)
	mux.HandleFunc("GET "+apiPrefix+"/requests", s.apiRequests)
	mux.HandleFunc("GET "+apiPrefix+"/requests/{id}", s.apiRequest)
	mux.HandleFunc("POST "+apiPrefix+"/requests/{id}/replay", s.apiReplay)
	mux.HandleFunc(apiPrefix+"/", func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, http.StatusNotFound, "unknown API endpoint "+r.Method+" "+r.URL.Path)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if token == "" || token == r.Header.Get("Authorization") {
			writeJSONError(w, http.StatusUnauthorized, "missing Authorization: Bearer <authtoken>")
			return
		}
		acct, err := s.store.AuthenticateToken(r.Context(), token)
		switch {
		case errors.Is(err, errNotFound):
			writeJSONError(w, http.StatusUnauthorized, "invalid authtoken")
			return
		case err != nil:
			logf("api auth: %v", err)
			writeJSONError(w, http.StatusServiceUnavailable, "server error, try again")
			return
		case acct.Disabled:
			writeJSONError(w, http.StatusForbidden, "account disabled")
			return
		case s.emailVerificationRequired(acct):
			writeJSONError(w, http.StatusForbidden, "verify your email address first")
			return
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accountKey{}, acct)))
	})
}

func apiAccount(r *http.Request) *Account { return r.Context().Value(accountKey{}).(*Account) }

func (s *Server) apiMe(w http.ResponseWriter, r *http.Request) {
	acct := apiAccount(r)
	u, err := s.store.User(r.Context(), acct.UserID)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	static, err := s.store.ListStatic(r.Context(), acct.UserID)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	hosts := make([]map[string]any, 0, len(static))
	for _, h := range static {
		hosts = append(hosts, map[string]any{"hostname": h.Hostname, "url": s.cfg.PublicURL(h.Hostname), "default": h.IsDefault})
	}
	teamRows, err := s.store.ListTeams(r.Context(), acct.UserID)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	teams := make([]map[string]string, 0, len(teamRows))
	for _, t := range teamRows {
		teams = append(teams, map[string]string{"slug": t.Slug, "name": t.Name, "role": t.Role})
	}
	rt := s.rt()
	limits := map[string]int{"tunnels": rt.MaxTunnelsPerUser, "pinned": rt.MaxPinnedPerUser, "domains": rt.MaxDomainsPerUser, "teams": rt.MaxTeamsPerUser}
	if u.IsAdmin {
		limits = map[string]int{"tunnels": 0, "pinned": 0, "domains": 0, "teams": 0}
	}
	m, err := s.meterFor(r.Context(), acct.UserID)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	m.mu.Lock()
	limits["bandwidth_kbps"], limits["transfer_gb"] = m.kbps, int(m.quotaBytes/1_000_000_000)
	m.mu.Unlock()
	start, end := monthBounds(time.Now())
	usage, err := s.store.MonthUsage(r.Context(), acct.UserID, start)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account":          map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "is_admin": u.IsAdmin},
		"server":           map[string]any{"base_domain": s.cfg.BaseDomain, "dashboard_url": s.cfg.DashboardURL(), "version": Version},
		"limits":           limits,
		"static_hostnames": hosts,
		"teams":            teams,
		"usage": map[string]any{
			"month_bytes_in": usage.BytesIn + m.pendingIn.Load(), "month_bytes_out": usage.BytesOut + m.pendingOut.Load(),
			"month_requests": usage.Requests + m.pendingReq.Load(), "month_connections": usage.Connections + m.pendingConn.Load(),
			"period_start": start, "period_end": end, "blocked": m.blocked.Load(),
		},
	})
}

func (s *Server) apiTunnels(w http.ResponseWriter, r *http.Request) {
	acct := apiAccount(r)
	rows, err := s.store.OnlineTunnels(r.Context(), acct.UserID)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	out := []map[string]any{}
	for _, t := range rows {
		out = append(out, map[string]any{
			"id": t.ID, "name": t.Name, "proto": t.Proto, "remote_port": t.RemotePort,
			"hostname": t.Hostname, "url": t.PublicURL, "local_addr": t.LocalAddr,
			"auth_mode": t.AuthMode, "static": t.Static, "started_at": t.StartedAt, "node": t.Node,
			"client": map[string]any{"hostname": t.ClientHostname, "os": t.ClientOS, "version": t.ClientVersion},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tunnels": out})
}

func (s *Server) apiStopTunnel(w http.ResponseWriter, r *http.Request) {
	acct := apiAccount(r)
	t := s.reg.ByID(r.PathValue("id"))
	if t == nil && s.stopRemote(r.Context(), r.PathValue("id"), acct.UserID, "stopped via the API") {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if t == nil || t.UserID != acct.UserID {
		writeJSONError(w, http.StatusNotFound, "no online tunnel with that id")
		return
	}
	t.session.unbind(t.BindID, "stopped via the API")
	writeJSON(w, http.StatusOK, map[string]any{})
}

type apiSummary struct {
	ID           string    `json:"id"`
	TunnelID     string    `json:"tunnel_id"`
	Hostname     string    `json:"hostname"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	Status       int       `json:"status"`
	DurationMS   float64   `json:"duration_ms"`
	TTFBMS       float64   `json:"ttfb_ms"`
	ReqBodySize  int64     `json:"req_body_size"`
	RespBodySize int64     `json:"resp_body_size"`
	RemoteAddr   string    `json:"remote_addr"`
	Error        string    `json:"error"`
	ReplayOf     string    `json:"replay_of"`
	StartedAt    time.Time `json:"started_at"`
}

func summary(r *RequestRow) apiSummary {
	return apiSummary{
		ID: r.ID, TunnelID: r.TunnelID, Hostname: r.Hostname, Method: r.Method, Path: r.Path, Status: r.Status,
		DurationMS: r.DurationMS, TTFBMS: r.TTFBMS, ReqBodySize: r.ReqBodySize, RespBodySize: r.RespBodySize,
		RemoteAddr: r.RemoteAddr, Error: r.Error, ReplayOf: r.ReplayOf, StartedAt: r.StartedAt,
	}
}

func (s *Server) apiRequests(w http.ResponseWriter, r *http.Request) {
	acct := apiAccount(r)
	q := r.URL.Query()
	f := RequestFilter{
		Hostname:    normalizeHost(q.Get("hostname")),
		TunnelID:    q.Get("tunnel_id"),
		Method:      q.Get("method"),
		StatusClass: q.Get("status"),
		Path:        q.Get("path"),
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeJSONError(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
		f.Limit = n
	}
	if v := q.Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "before must be an RFC 3339 timestamp")
			return
		}
		f.Before = t
	}
	switch f.StatusClass {
	case "", "2xx", "3xx", "4xx", "5xx":
	default:
		writeJSONError(w, http.StatusBadRequest, "status must be 2xx, 3xx, 4xx or 5xx")
		return
	}
	if f.Limit == 0 {
		f.Limit = 50
	}
	rows, err := s.store.ListRequests(r.Context(), acct.UserID, f)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	out := make([]apiSummary, 0, len(rows))
	for i := range rows {
		out = append(out, summary(&rows[i]))
	}
	next := ""
	if len(rows) == f.Limit {
		next = rows[len(rows)-1].StartedAt.Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out, "next_before": next})
}

func (s *Server) apiConnections(w http.ResponseWriter, r *http.Request) {
	acct := apiAccount(r)
	q := r.URL.Query()
	f := ConnFilter{TunnelID: q.Get("tunnel_id"), Address: strings.ToLower(q.Get("address")), Limit: 50}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeJSONError(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
		f.Limit = n
	}
	if v := q.Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "before must be an RFC 3339 timestamp")
			return
		}
		f.Before = t
	}
	rows, err := s.store.ListConnections(r.Context(), acct.UserID, f)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	next := ""
	if len(rows) == f.Limit {
		next = rows[len(rows)-1].StartedAt.Format(time.RFC3339Nano)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		out = append(out, map[string]any{
			"id": c.ID, "tunnel_id": c.TunnelID, "proto": c.Proto, "address": c.Address, "remote_addr": c.RemoteAddr,
			"bytes_in": c.BytesIn, "bytes_out": c.BytesOut, "duration_ms": ms(c.Duration), "error": c.Error, "started_at": c.StartedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out, "next_before": next})
}

func (s *Server) apiRequest(w http.ResponseWriter, r *http.Request) {
	acct := apiAccount(r)
	row, err := s.store.RequestDetail(r.Context(), acct.UserID, r.PathValue("id"))
	if errors.Is(err, errNotFound) {
		writeJSONError(w, http.StatusNotFound, "request not found")
		return
	}
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		apiSummary
		Proto    string         `json:"proto"`
		Request  map[string]any `json:"request"`
		Response map[string]any `json:"response"`
	}{
		apiSummary: summary(row),
		Proto:      row.Proto,
		Request:    map[string]any{"headers": nonNil(row.ReqHeaders), "body": apiBody(row.ReqBody, row.ReqBodySize, row.ReqBodyTruncated, row.ReqHeaders)},
		Response:   map[string]any{"headers": nonNil(row.RespHeaders), "body": apiBody(row.RespBody, row.RespBodySize, row.RespBodyTruncated, row.RespHeaders)},
	})
}

func (s *Server) apiReplay(w http.ResponseWriter, r *http.Request) {
	acct := apiAccount(r)
	var o *replayOverride
	if r.ContentLength != 0 {
		o = &replayOverride{}
		if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(o); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
	}
	id, status, err := s.replay(r.Context(), acct.UserID, r.PathValue("id"), o)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request_id": id, "status": status})
}

func nonNil(h http.Header) http.Header {
	if h == nil {
		return http.Header{}
	}
	return h
}

type apiBodyJSON struct {
	Size            int64  `json:"size"`
	Truncated       bool   `json:"truncated"`
	ContentType     string `json:"content_type"`
	ContentEncoding string `json:"content_encoding"`
	Decoded         bool   `json:"decoded"`
	Text            string `json:"text,omitempty"`
	Base64          string `json:"base64,omitempty"`
}

// maxDecoded bounds decompression so a captured zip bomb cannot exhaust memory.
const maxDecoded = 8 << 20

func apiBody(raw []byte, size int64, truncated bool, h http.Header) apiBodyJSON {
	b := apiBodyJSON{Size: size, Truncated: truncated, ContentType: h.Get("Content-Type"), ContentEncoding: h.Get("Content-Encoding")}
	if len(raw) == 0 {
		return b
	}
	data := raw
	if enc := strings.ToLower(strings.TrimSpace(b.ContentEncoding)); enc != "" && enc != "identity" {
		if dec, ok := decodeBody(enc, raw, truncated); ok {
			data, b.Decoded = dec, true
		}
	}
	if looksLikeText(b.ContentType, data, truncated) {
		b.Text = string(data)
	} else {
		b.Base64 = base64.StdEncoding.EncodeToString(raw)
	}
	return b
}

// decodeBody undoes a Content-Encoding. A truncated capture decodes as far as
// it goes.
func decodeBody(enc string, raw []byte, truncated bool) ([]byte, bool) {
	var r io.Reader
	switch enc {
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, false
		}
		r = zr
	case "deflate":
		// "deflate" is zlib-wrapped per the RFC, raw deflate in practice too.
		if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			r = zr
		} else {
			r = flate.NewReader(bytes.NewReader(raw))
		}
	case "br":
		r = brotli.NewReader(bytes.NewReader(raw))
	case "zstd":
		zr, err := zstd.NewReader(bytes.NewReader(raw), zstd.WithDecoderMaxMemory(maxDecoded))
		if err != nil {
			return nil, false
		}
		defer zr.Close()
		r = zr
	default:
		return nil, false
	}
	out, err := io.ReadAll(io.LimitReader(r, maxDecoded))
	if err != nil && !(truncated && len(out) > 0) {
		return nil, false
	}
	return out, true
}

func looksLikeText(contentType string, data []byte, truncated bool) bool {
	if truncated {
		// The capture may end in the middle of a multi-byte character.
		for i := 0; i < 3 && len(data) > 0 && !utf8.Valid(data); i++ {
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	mt, _, _ := mime.ParseMediaType(contentType)
	switch {
	case mt == "", strings.HasPrefix(mt, "text/"):
		return true
	case strings.Contains(mt, "json"), strings.Contains(mt, "xml"), strings.Contains(mt, "javascript"),
		strings.Contains(mt, "yaml"), strings.Contains(mt, "graphql"), strings.Contains(mt, "csv"),
		mt == "application/x-www-form-urlencoded", mt == "application/x-ndjson":
		return true
	}
	return false
}
