package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Device authorization for `tund login` (see docs/SPEC.md).

const (
	deviceCodeTTL    = 10 * time.Minute
	devicePollPeriod = 2
	userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"
)

var (
	hexHash       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	tokenPrefix   = regexp.MustCompile(`^tund_[0-9a-f]{8}$`)
	callbackState = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
)

func newUserCode() string {
	var b strings.Builder
	for i := range 8 {
		if i == 4 {
			b.WriteByte('-')
		}
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(userCodeAlphabet))))
		b.WriteByte(userCodeAlphabet[n.Int64()])
	}
	return b.String()
}

func (s *Server) handleDeviceCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var in struct {
		TokenHash      string `json:"token_hash"`
		TokenPrefix    string `json:"token_prefix"`
		ClientHostname string `json:"client_hostname"`
		ClientOS       string `json:"client_os"`
		CallbackPort   int    `json:"callback_port"`
		CallbackState  string `json:"callback_state"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil || !hexHash.MatchString(in.TokenHash) || !tokenPrefix.MatchString(in.TokenPrefix) {
		writeJSONError(w, http.StatusBadRequest, "token_hash (sha256 hex) and token_prefix are required")
		return
	}
	if in.CallbackPort != 0 && (in.CallbackPort < 1024 || in.CallbackPort > 65535 || !callbackState.MatchString(in.CallbackState)) {
		writeJSONError(w, http.StatusBadRequest, "callback_port must be 1024-65535 and callback_state 16-128 URL-safe characters")
		return
	}
	if !s.deviceLimiter.allow(clientIP(r)) {
		writeJSONError(w, http.StatusTooManyRequests, "too many login attempts from this address; wait a few minutes")
		return
	}
	deviceCode := randomToken(32)
	row := DeviceCodeRow{
		DeviceCodeHash: sha256Hex(deviceCode),
		TokenHash:      in.TokenHash,
		TokenPrefix:    in.TokenPrefix,
		ClientHostname: truncate(in.ClientHostname, 120),
		ClientOS:       truncate(in.ClientOS, 60),
		ClientIP:       clientIP(r),
		ExpiresAt:      time.Now().Add(deviceCodeTTL),
	}
	if in.CallbackPort != 0 {
		row.CallbackPort, row.CallbackState = in.CallbackPort, in.CallbackState
	}
	var err error
	for range 5 {
		row.UserCode = newUserCode()
		if err = s.store.CreateDeviceCode(r.Context(), row); err == nil {
			break
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" { // unique_violation: retry with a new user code
			break
		}
	}
	if err != nil {
		logf("device code: %v", err)
		writeJSONError(w, http.StatusServiceUnavailable, "could not start login, try again")
		return
	}
	verify := s.cfg.DashboardURL() + "/device"
	out := map[string]any{
		"device_code":               deviceCode,
		"user_code":                 row.UserCode,
		"verification_url":          verify,
		"verification_url_complete": verify + "?code=" + row.UserCode,
		"interval":                  devicePollPeriod,
		"expires_in":                int(deviceCodeTTL.Seconds()),
	}
	if row.CallbackPort != 0 {
		// Tells the CLI the callback was accepted, and where to send the
		// browser once it has redeemed the callback code.
		out["callback_done_url"] = verify + "/done"
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var in struct {
		DeviceCode   string `json:"device_code"`
		CallbackCode string `json:"callback_code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil || in.DeviceCode == "" {
		writeJSONError(w, http.StatusBadRequest, "device_code is required")
		return
	}
	if in.CallbackCode != "" {
		s.redeemDeviceCallback(w, r, in.DeviceCode, in.CallbackCode)
		return
	}
	res, err := s.store.PollDeviceCode(r.Context(), sha256Hex(in.DeviceCode))
	switch {
	case errors.Is(err, errNotFound):
		writeJSONError(w, http.StatusGone, "expired_token")
	case err != nil:
		logf("device token: %v", err)
		writeJSONError(w, http.StatusServiceUnavailable, "server error, keep polling")
	case res.Expired:
		writeJSONError(w, http.StatusGone, "expired_token")
	case res.Status == "approved":
		writeJSON(w, http.StatusOK, map[string]string{"status": "approved", "account": res.Account})
	case res.Status == "denied":
		writeJSONError(w, http.StatusForbidden, "access_denied")
	default:
		writeJSONError(w, http.StatusPreconditionRequired, "authorization_pending")
	}
}

// redeemDeviceCallback finishes a loopback login: the CLI presents the code
// the dashboard put into the browser redirect to 127.0.0.1, which creates the
// token. Answers like the poll, plus 400 invalid_callback_code (stale or wrong
// code; the CLI keeps waiting).
func (s *Server) redeemDeviceCallback(w http.ResponseWriter, r *http.Request, deviceCode, callbackCode string) {
	account, err := s.store.RedeemDeviceCallback(r.Context(), sha256Hex(deviceCode), sha256Hex(callbackCode))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "approved", "account": account})
	case errors.Is(err, errNotFound), errors.Is(err, errDeviceExpired):
		writeJSONError(w, http.StatusGone, "expired_token")
	case errors.Is(err, errDeviceDenied):
		writeJSONError(w, http.StatusForbidden, "access_denied")
	case errors.Is(err, errDeviceCallback):
		writeJSONError(w, http.StatusBadRequest, "invalid_callback_code")
	default:
		logf("device callback: %v", err)
		writeJSONError(w, http.StatusServiceUnavailable, "server error, try again")
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
