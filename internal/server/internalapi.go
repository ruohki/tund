package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"tund/internal/protocol"
)

// internalAPI serves the dashboard-only endpoints on TUND_INTERNAL_ADDR.
func (s *Server) internalAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"tunnels": len(s.reg.Tunnels()), "sessions": len(s.reg.Sessions())})
	})
	mux.HandleFunc("GET /internal/status", s.handleStatus)
	mux.HandleFunc("POST /internal/admin/tunnels/stop", s.handleAdminStop)
	mux.HandleFunc("POST /internal/replay", s.handleReplay)
	mux.HandleFunc("POST /internal/tunnels/stop", s.handleStopTunnel)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/internal/health" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.InternalSecret)) != 1 {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

type replayRequest struct {
	RequestID string          `json:"request_id"`
	UserID    string          `json:"user_id"`
	Override  *replayOverride `json:"override"`
}

// replayOverride changes parts of a captured request before replaying it.
type replayOverride struct {
	// Hostname sends the request through another online tunnel the user may
	// replay through (one of theirs, or of a team domain they belong to).
	Hostname   string      `json:"hostname"`
	Method     string      `json:"method"`
	Path       string      `json:"path"`
	Headers    http.Header `json:"headers"`
	Body       *string     `json:"body"`
	BodyBase64 *string     `json:"body_base64"`
}

// apiError carries an HTTP status for API handlers.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func writeAPIError(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeJSONError(w, ae.status, ae.msg)
		return
	}
	logf("api: %v", err)
	writeJSONError(w, http.StatusInternalServerError, "internal server error")
}

// hopHeaders must not be replayed verbatim.
var hopHeaders = []string{"Connection", "Keep-Alive", "Proxy-Connection", "Transfer-Encoding", "Upgrade", "Te", "Trailer", "Content-Length"}

func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	var in replayRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&in); err != nil || in.RequestID == "" || in.UserID == "" {
		writeJSONError(w, http.StatusBadRequest, "request_id and user_id are required")
		return
	}
	id, status, err := s.replay(r.Context(), in.UserID, in.RequestID, in.Override)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request_id": id, "status": status})
}

// replay sends a captured request through its hostname's tunnel again and
// returns the id of the new capture and the response status.
func (s *Server) replay(ctx context.Context, userID, requestID string, o *replayOverride) (string, int, error) {
	orig, err := s.store.Request(ctx, requestID, userID)
	if errors.Is(err, errNotFound) {
		return "", 0, &apiError{http.StatusNotFound, "request not found"}
	}
	if err != nil {
		return "", 0, err
	}
	target := orig.Hostname
	if o != nil && o.Hostname != "" {
		target = normalizeHost(o.Hostname)
	}
	t := s.reg.Lookup(target)
	if t == nil && s.cluster != nil {
		// The tunnel lives on another node: replay there.
		if n, proto, ok := s.cluster.ownerOf(ctx, target); ok && proto == protocol.ProtoHTTP {
			reply, err := s.cluster.command(ctx, n, relayCmd{Cmd: "replay", UserID: userID, RequestID: requestID, Override: o})
			if err != nil {
				return "", 0, err
			}
			if !reply.OK {
				return "", 0, &apiError{http.StatusConflict, reply.Error}
			}
			return reply.RequestID, reply.Status, nil
		}
	}
	if t == nil || t.Proto != protocol.ProtoHTTP || !s.mayReplayThrough(ctx, userID, t) {
		return "", 0, &apiError{http.StatusConflict, "no HTTP tunnel of yours is online for " + target}
	}

	method, path, headers, body := orig.Method, orig.Path, orig.Headers, orig.Body
	if o != nil {
		if o.Method != "" {
			method = strings.ToUpper(o.Method)
		}
		if o.Path != "" {
			path = o.Path
		}
		if o.Headers != nil {
			headers = o.Headers
		}
		switch {
		case o.BodyBase64 != nil:
			if body, err = base64.StdEncoding.DecodeString(*o.BodyBase64); err != nil {
				return "", 0, &apiError{http.StatusBadRequest, "body_base64 is not valid base64"}
			}
		case o.Body != nil:
			body = []byte(*o.Body)
		}
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, s.cfg.PublicURL(target)+path, bytes.NewReader(body))
	if err != nil {
		return "", 0, &apiError{http.StatusBadRequest, "invalid request: " + err.Error()}
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	for _, h := range hopHeaders {
		req.Header.Del(h)
	}
	req.Header.Del("X-Forwarded-For")
	req.Host = target
	req.RemoteAddr = "replay:0"
	req.RequestURI = path
	if len(body) == 0 {
		req.Body = http.NoBody
	}

	rec := &discardWriter{header: http.Header{}}
	id := s.serveTunnel(rec, withIdentity(req, identityFromHeaders(orig.Headers)), t, orig.ID)
	return id, rec.code, nil
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	certs := []map[string]any{}
	for _, name := range s.certs.StatusNames() {
		for _, c := range s.certs.Info(name) {
			certs = append(certs, c)
		}
	}
	tcp := map[string]any{}
	if s.tcp != nil {
		tcp = map[string]any{"from": s.tcp.from, "to": s.tcp.to}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":        Version,
		"started_at":     s.startedAt,
		"base_domain":    s.cfg.BaseDomain,
		"dashboard_host": s.cfg.DashboardHost,
		"tls_mode":       s.cfg.TLSMode,
		"dns_provider":   s.cfg.DNSProvider,
		"tunnels":        len(s.reg.Tunnels()),
		"sessions":       len(s.reg.Sessions()),
		"recorder_queue": len(s.recorder.ch),
		"certificates":   certs,
		"tcp_ports":      tcp,
		"tcp_host":       s.tcpHost(),
		"node":           s.cfg.NodeName(),
		"nodes":          s.nodeStatus(r.Context()),
	})
}

func (s *Server) handleAdminStop(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TunnelID string `json:"tunnel_id"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || in.TunnelID == "" {
		writeJSONError(w, http.StatusBadRequest, "tunnel_id is required")
		return
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "stopped by an administrator"
	}
	t := s.reg.ByID(in.TunnelID)
	if t == nil && s.stopRemote(r.Context(), in.TunnelID, "", reason) {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if t == nil {
		s.store.EndTunnel(r.Context(), in.TunnelID)
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	t.session.unbind(t.BindID, reason)
	logf("tunnel %s stopped by an administrator: %s", t.Hostname, reason)
	writeJSON(w, http.StatusOK, map[string]any{})
}

// stopRemote asks the node holding a tunnel to stop it (userID "" = any owner).
func (s *Server) stopRemote(ctx context.Context, tunnelID, userID, reason string) bool {
	if s.cluster == nil {
		return false
	}
	nodeName, err := s.store.TunnelNodeByID(ctx, tunnelID)
	if err != nil || nodeName == s.cluster.name {
		return false
	}
	n, ok := s.cluster.node(nodeName)
	if !ok {
		return false
	}
	reply, err := s.cluster.command(ctx, n, relayCmd{Cmd: "stop", TunnelID: tunnelID, UserID: userID, Reason: reason})
	return err == nil && reply.OK
}

// mayReplayThrough: your own tunnels, or a teammate's tunnel on a hostname
// owned by a team you both belong to.
func (s *Server) mayReplayThrough(ctx context.Context, userID string, t *Tunnel) bool {
	if t.UserID == userID {
		return true
	}
	ok, err := s.store.ShareTeamDomain(ctx, t.Hostname, userID, t.UserID)
	if err != nil {
		logf("replay permission: %v", err)
	}
	return ok
}

// discardWriter receives replayed responses; the capture already has the body.
type discardWriter struct {
	header http.Header
	code   int
}

func (d *discardWriter) Header() http.Header { return d.header }
func (d *discardWriter) Write(b []byte) (int, error) {
	if d.code == 0 {
		d.code = http.StatusOK
	}
	return len(b), nil
}
func (d *discardWriter) WriteHeader(code int) {
	if d.code == 0 {
		d.code = code
	}
}
func (d *discardWriter) Flush() {}

func (s *Server) handleStopTunnel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TunnelID string `json:"tunnel_id"`
		UserID   string `json:"user_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || in.TunnelID == "" {
		writeJSONError(w, http.StatusBadRequest, "tunnel_id is required")
		return
	}
	t := s.reg.ByID(in.TunnelID)
	if t == nil && s.stopRemote(r.Context(), in.TunnelID, in.UserID, "stopped from the dashboard") {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if t == nil {
		// Already gone: make sure the database agrees.
		s.store.EndTunnelOwned(r.Context(), in.TunnelID, in.UserID)
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if t.UserID != in.UserID {
		writeJSONError(w, http.StatusNotFound, "tunnel not found")
		return
	}
	t.session.unbind(t.BindID, "stopped from the dashboard")
	writeJSON(w, http.StatusOK, map[string]any{})
}
