package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tund/internal/protocol"
)

// The newest client release, read from version.txt next to the binaries
// (TUND_DOWNLOADS_DIR, else TUND_DOWNLOAD_BASE_URL) and refreshed hourly.
// Clients that connect with an older version get a notice.

const clientVersionRefresh = time.Hour

func (s *Server) latestClient() string {
	if v := s.latestClientVersion.Load(); v != nil {
		return *v
	}
	return ""
}

func (s *Server) latestClientLoop(ctx context.Context) {
	for {
		v, err := s.fetchLatestClient(ctx)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				logf("latest client version: %v", err)
			}
		case v != s.latestClient():
			s.latestClientVersion.Store(&v)
			logf("latest client release: %s", v)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(clientVersionRefresh):
		}
	}
}

func (s *Server) fetchLatestClient(ctx context.Context) (string, error) {
	var raw []byte
	if s.cfg.DownloadsDir != "" {
		if b, err := os.ReadFile(filepath.Join(s.cfg.DownloadsDir, "version.txt")); err == nil {
			raw = b
		}
	}
	if raw == nil {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.DownloadBaseURL+"/version.txt", nil)
		if err != nil {
			return "", err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("%s/version.txt: HTTP %d", s.cfg.DownloadBaseURL, resp.StatusCode)
		}
		if raw, err = io.ReadAll(io.LimitReader(resp.Body, 64)); err != nil {
			return "", err
		}
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(raw)), "v")
	if _, ok := protocol.ParseVersion(v); !ok {
		return "", fmt.Errorf("version.txt holds %q, not a version", v)
	}
	return v, nil
}

// updateNotice is the notice for a client older than the latest release, or
// nil. Error carries the text for clients without `tund update`: the
// installer one-liner for their OS.
func (s *Server) updateNotice(clientVersion, clientOS string) *protocol.Message {
	latest := s.latestClient()
	if !protocol.NewerVersion(latest, clientVersion) {
		return nil
	}
	install := "curl -fsSL " + s.cfg.DashboardURL() + "/install.sh | sh"
	if strings.HasPrefix(clientOS, "windows") {
		install = "irm " + s.cfg.DashboardURL() + "/install.ps1 | iex"
	}
	return &protocol.Message{
		Type:          protocol.TypeNotice,
		Error:         fmt.Sprintf("tund %s is available (you have %s). Update with: %s", latest, clientVersion, install),
		UpdateVersion: latest,
	}
}
