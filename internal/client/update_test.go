package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestChecksumFor(t *testing.T) {
	sums := []byte("" +
		"1111111111111111111111111111111111111111111111111111111111111111  tund-linux-amd64\n" +
		"2222222222222222222222222222222222222222222222222222222222222222 *tund-windows-amd64.exe\n" +
		"33  tund-darwin-arm64\n")
	if got := checksumFor(sums, "tund-linux-amd64"); !strings.HasPrefix(got, "1111") {
		t.Errorf("linux = %q", got)
	}
	if got := checksumFor(sums, "tund-windows-amd64.exe"); !strings.HasPrefix(got, "2222") {
		t.Errorf("windows (binary mode) = %q", got)
	}
	if got := checksumFor(sums, "tund-darwin-arm64"); got != "" {
		t.Errorf("short hash accepted: %q", got)
	}
	if got := checksumFor(sums, "tund-linux"); got != "" {
		t.Errorf("prefix matched: %q", got)
	}
}

// fakeRelease serves /_tund/downloads/ like tund-server: version.txt,
// checksums.txt and this platform's binary (a shell script printing its version).
type fakeRelease struct {
	version string
	binary  []byte
	sum     string // "" = the real checksum
}

func (f *fakeRelease) handler(t *testing.T) http.Handler {
	name, err := BinaryName()
	if err != nil {
		t.Skip(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, downloadsPath) {
		case "version.txt":
			fmt.Fprintln(w, f.version)
		case "checksums.txt":
			sum := f.sum
			if sum == "" {
				h := sha256.Sum256(f.binary)
				sum = hex.EncodeToString(h[:])
			}
			fmt.Fprintf(w, "%s  %s\n%s  tund-other-os\n", sum, name, strings.Repeat("0", 64))
		case name:
			w.Write(f.binary)
		default:
			http.NotFound(w, r)
		}
	})
}

func script(version string) []byte {
	return []byte("#!/bin/sh\necho 'tund " + version + " (test build)'\n")
}

func installed(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "tund")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	old := Version
	defer func() { Version = old }()
	Version = "0.3.0"

	f := &fakeRelease{version: "v0.4.0", binary: script("0.4.0")}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()

	if v, err := LatestVersion(context.Background(), ts.URL, nil); err != nil || v != "0.4.0" {
		t.Fatalf("LatestVersion = %q, %v", v, err)
	}

	exe := installed(t)
	var said []string
	res, err := Update(context.Background(), UpdateOptions{Server: ts.URL, Executable: exe, Progress: func(s string) { said = append(said, s) }})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Updated || res.From != "0.3.0" || res.To != "0.4.0" || len(said) == 0 {
		t.Fatalf("result %+v, progress %q", res, said)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(f.binary) {
		t.Fatalf("executable not replaced: %q", b)
	}
	if st, _ := os.Stat(exe); st.Mode().Perm()&0o111 == 0 {
		t.Errorf("new binary not executable: %v", st.Mode())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".tund-update-*")); len(left) > 0 {
		t.Errorf("staging files left behind: %v", left)
	}

	// Current already: nothing to do.
	Version = "0.4.0"
	if res, err := Update(context.Background(), UpdateOptions{Server: ts.URL, Executable: installed(t)}); err != nil || res.Updated {
		t.Errorf("up to date: %+v, %v", res, err)
	}

	// Development builds only with --force.
	Version = "dev"
	if _, err := Update(context.Background(), UpdateOptions{Server: ts.URL, Executable: installed(t)}); !errors.Is(err, ErrDevBuild) {
		t.Errorf("dev build: %v", err)
	}
	if res, err := Update(context.Background(), UpdateOptions{Server: ts.URL, Executable: installed(t), Force: true}); err != nil || !res.Updated {
		t.Errorf("dev build with force: %+v, %v", res, err)
	}
}

func TestUpdateRefusesBadBinaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	old := Version
	defer func() { Version = old }()
	Version = "0.3.0"

	for name, f := range map[string]*fakeRelease{
		"checksum mismatch": {version: "0.4.0", binary: script("0.4.0"), sum: strings.Repeat("a", 64)},
		"wrong version":     {version: "0.4.0", binary: script("0.3.9")},
		"doesn't run":       {version: "0.4.0", binary: []byte("\x7fELF garbage")},
	} {
		ts := httptest.NewServer(f.handler(t))
		exe := installed(t)
		_, err := Update(context.Background(), UpdateOptions{Server: ts.URL, Executable: exe})
		ts.Close()
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if b, _ := os.ReadFile(exe); string(b) != "old binary" {
			t.Errorf("%s: executable changed", name)
		}
		if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".tund-update-*")); len(left) > 0 {
			t.Errorf("%s: staging files left behind: %v", name, left)
		}
	}
}

func TestUpdatePermission(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory this user can't write")
	}
	old := Version
	defer func() { Version = old }()
	Version = "0.3.0"
	f := &fakeRelease{version: "0.4.0", binary: script("0.4.0")}
	ts := httptest.NewServer(f.handler(t))
	defer ts.Close()

	exe := installed(t)
	dir := filepath.Dir(exe)
	os.Chmod(dir, 0o555)
	defer os.Chmod(dir, 0o755)
	resolved, _ := filepath.EvalSymlinks(exe) // Update names the real file (/var → /private/var on macOS)
	var perm *PermissionError
	if _, err := Update(context.Background(), UpdateOptions{Server: ts.URL, Executable: exe}); !errors.As(err, &perm) || perm.Path != resolved {
		t.Fatalf("err = %v", err)
	}
}

func TestLatestVersionOldServer(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	if _, err := LatestVersion(context.Background(), ts.URL, nil); err == nil || !strings.Contains(err.Error(), "doesn't publish") {
		t.Fatalf("err = %v", err)
	}
}
