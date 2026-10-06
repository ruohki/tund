package fileserve

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFinderHidden(t *testing.T) {
	dir := makeShare(t, map[string]string{"Library/x.txt": "x", "Visible/y.txt": "y", "hidden.txt": "h"})
	for _, n := range []string{"Library", "hidden.txt"} {
		if err := unix.Chflags(filepath.Join(dir, n), unix.UF_HIDDEN); err != nil {
			t.Fatal(err)
		}
	}
	s := newServer(t, Options{Path: dir, Upload: true})
	if names := listNames(t, s, "/"); !slices.Equal(names, []string{"Visible"}) {
		t.Errorf("listing = %q", names)
	}
	for _, p := range []string{"/Library/", "/Library/x.txt", "/hidden.txt"} {
		if w := get(s, p, ""); w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d", p, w.Code)
		}
	}
	if w := do(s, request("PUT", "/Library/z.txt", strings.NewReader("z"))); w.Code != http.StatusNotFound {
		t.Errorf("PUT into a hidden folder = %d", w.Code)
	}
}

func TestPlaceholderFlag(t *testing.T) {
	if !placeholder(fakeInfo{&syscall.Stat_t{Flags: unix.SF_DATALESS}}) || placeholder(fakeInfo{&syscall.Stat_t{}}) {
		t.Error("placeholder() misreads SF_DATALESS")
	}
}

func TestUploadsAreQuarantined(t *testing.T) {
	dir := makeShare(t, nil)
	s := newServer(t, Options{Path: dir, Upload: true})
	if w := do(s, request("PUT", "/tool.dmg", strings.NewReader("x"))); w.Code != http.StatusCreated {
		t.Fatalf("PUT = %d", w.Code)
	}
	buf := make([]byte, 128)
	n, err := unix.Getxattr(filepath.Join(dir, "tool.dmg"), "com.apple.quarantine", buf)
	if err != nil {
		t.Fatalf("no quarantine attribute: %v", err)
	}
	if v := string(buf[:n]); !strings.HasPrefix(v, "0081;") || !strings.HasSuffix(v, ";tund;") {
		t.Errorf("quarantine = %q", v)
	}
}

func TestCheckRootUploadAutostart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := unix.Chflags(filepath.Join(home, "Library"), unix.UF_HIDDEN); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckRoot(agents, false); err != nil {
		t.Errorf("read-only share of LaunchAgents: %v", err)
	}
	want := "refusing --upload for ~/Library/LaunchAgents: programs start from folders inside Library. Share another folder for uploads"
	if _, err := CheckRoot(agents, true); err == nil || err.Error() != want {
		t.Errorf("CheckRoot(upload) = %v\nwant %s", err, want)
	}
	if _, err := CheckRoot(filepath.Join(home, "Library"), true); err == nil || !strings.Contains(err.Error(), "inside Library") {
		t.Errorf("CheckRoot(~/Library, upload) = %v", err)
	}
}
