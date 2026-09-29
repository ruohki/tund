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
	hexHash     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	tokenPrefix = regexp.MustCompile(`^tund_[0-9a-f]{8}$`)
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
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil || !hexHash.MatchString(in.TokenHash) || !tokenPrefix.MatchString(in.TokenPrefix) {
		writeJSONError(w, http.StatusBadRequest, "token_hash (sha256 hex) and token_prefix are required")
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
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               deviceCode,
		"user_code":                 row.UserCode,
		"verification_url":          verify,
		"verification_url_complete": verify + "?code=" + row.UserCode,
		"interval":                  devicePollPeriod,
		"expires_in":                int(deviceCodeTTL.Seconds()),
	})
}

func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var in struct {
		DeviceCode string `json:"device_code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&in); err != nil || in.DeviceCode == "" {
		writeJSONError(w, http.StatusBadRequest, "device_code is required")
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

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
