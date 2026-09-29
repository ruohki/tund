package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateNotice(t *testing.T) {
	s := &Server{cfg: &Config{BaseDomain: "tund.example", DashboardHost: "tund.example", PublicScheme: "https"}}
	if s.updateNotice("0.1.0", "linux/amd64") != nil {
		t.Fatal("notice without a known latest version")
	}
	latest := "0.4.0"
	s.latestClientVersion.Store(&latest)
	for _, current := range []string{"0.4.0", "0.5.0", "dev", ""} {
		if n := s.updateNotice(current, "linux/amd64"); n != nil {
			t.Errorf("client %q got a notice: %+v", current, n)
		}
	}
	n := s.updateNotice("0.3.0", "darwin/arm64")
	if n == nil || n.UpdateVersion != "0.4.0" || !strings.Contains(n.Error, "0.3.0") || !strings.Contains(n.Error, "/install.sh | sh") {
		t.Fatalf("notice = %+v", n)
	}
	if n := s.updateNotice("0.3.0", "windows/amd64"); n == nil || !strings.Contains(n.Error, "install.ps1 | iex") {
		t.Fatalf("windows notice = %+v", n)
	}
}

func TestFetchLatestClient(t *testing.T) {
	body := "v0.4.0\n"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version.txt" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	defer ts.Close()
	s := &Server{cfg: &Config{DownloadBaseURL: ts.URL}}
	if v, err := s.fetchLatestClient(context.Background()); err != nil || v != "0.4.0" {
		t.Fatalf("fetch = %q, %v", v, err)
	}
	body = "<html>not found</html>"
	if v, err := s.fetchLatestClient(context.Background()); err == nil {
		t.Fatalf("accepted %q", v)
	}

	// A local downloads directory wins over the base URL.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "version.txt"), []byte("0.5.1\n"), 0o644)
	s.cfg.DownloadsDir = dir
	if v, err := s.fetchLatestClient(context.Background()); err != nil || v != "0.5.1" {
		t.Fatalf("local fetch = %q, %v", v, err)
	}
}
