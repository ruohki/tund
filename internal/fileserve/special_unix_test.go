//go:build unix

package fileserve

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// within fails the test if fn takes longer than d, and never hangs on it.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	start := time.Now()
	go func() { fn(); close(done) }()
	select {
	case <-done:
		if took := time.Since(start); took > d {
			t.Errorf("%s took %v", what, took)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s blocked", what)
	}
}

func TestSpecialFiles(t *testing.T) {
	dir := makeShare(t, map[string]string{"a.txt": "a", "b.txt": "b"})
	for _, name := range []string{"pipe", "x.pem", "fifo.txt"} {
		if err := syscall.Mkfifo(filepath.Join(dir, name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir) // socket paths are short this way
	l, err := net.Listen("unix", "sock")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	symlink(t, "pipe", filepath.Join(dir, "pipe-link.txt"))
	s := newServer(t, Options{Path: dir})

	within(t, 200*time.Millisecond, "listing", func() {
		if names := listNames(t, s, "/"); !slices.Equal(names, []string{"a.txt", "b.txt"}) {
			t.Errorf("listing = %q", names)
		}
	})
	for _, p := range []string{"/pipe", "/x.pem", "/fifo.txt", "/sock", "/pipe-link.txt", "/pipe/"} {
		within(t, 200*time.Millisecond, "GET "+p, func() {
			if w := get(s, p, ""); w.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d", p, w.Code)
			}
		})
	}
	// A FIFO that replaced a listed file.
	os.Remove(filepath.Join(dir, "b.txt"))
	syscall.Mkfifo(filepath.Join(dir, "b.txt"), 0o644)
	within(t, 200*time.Millisecond, "GET swapped FIFO", func() {
		if w := get(s, "/b.txt", ""); w.Code != http.StatusNotFound {
			t.Errorf("GET /b.txt = %d", w.Code)
		}
	})
	// Even when it slips past resolution, opening it can't block.
	info := mustLstat(t, filepath.Join(dir, "pipe"))
	within(t, 200*time.Millisecond, "open FIFO", func() {
		if _, _, err := s.open(node{segs: []string{"pipe"}, rel: "pipe", info: info}); err == nil {
			t.Error("opened a FIFO")
		}
	})
}

func TestSingleFileBecomesFIFO(t *testing.T) {
	dir := makeShare(t, map[string]string{"report.txt": "r"})
	s := newServer(t, Options{Path: filepath.Join(dir, "report.txt")})
	os.Remove(filepath.Join(dir, "report.txt"))
	syscall.Mkfifo(filepath.Join(dir, "report.txt"), 0o644)
	for _, accept := range []string{"", "text/html"} {
		within(t, 200*time.Millisecond, "GET /", func() {
			if w := get(s, "/", accept); w.Code != http.StatusNotFound {
				t.Errorf("GET / (%q) = %d", accept, w.Code)
			}
		})
	}
}

func TestUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := makeShare(t, map[string]string{"locked/a.txt": "a", "private.txt": "p"})
	s := newServer(t, Options{Path: dir})
	os.Chmod(filepath.Join(dir, "private.txt"), 0)
	os.Chmod(filepath.Join(dir, "locked"), 0)
	t.Cleanup(func() { os.Chmod(filepath.Join(dir, "locked"), 0o755) })
	for p, code := range map[string]int{"/private.txt": 403, "/locked/": 403, "/locked/a.txt": 404} {
		for _, accept := range []string{"", "application/json", "text/html"} {
			w := get(s, p, accept)
			if w.Code != code {
				t.Errorf("GET %s (%s) = %d, want %d", p, accept, w.Code, code)
			}
			real, _ := filepath.EvalSymlinks(dir)
			if body := w.Body.String(); strings.Contains(body, real) || strings.Contains(body, dir) || strings.Contains(body, "permission denied") {
				t.Errorf("GET %s leaks OS details: %q", p, body)
			}
		}
	}
}

func TestCheckRootSpecial(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	syscall.Mkfifo(fifo, 0o644)
	if _, err := CheckRoot(fifo, false); err == nil || err.Error() != fifo+" is not a regular file or folder" {
		t.Errorf("CheckRoot(FIFO) = %v", err)
	}
	if os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked")
		os.Mkdir(locked, 0)
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
		if _, err := CheckRoot(locked, false); err == nil || !strings.HasPrefix(err.Error(), "cannot read "+locked+": permission denied") {
			t.Errorf("CheckRoot(unreadable) = %v", err)
		}
	}
}
