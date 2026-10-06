package fileserve

import (
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestClassifyWindows(t *testing.T) {
	for _, c := range []struct {
		err  error
		want errKind
	}{
		{windows.ERROR_FILE_NOT_FOUND, errGone},
		{&fs.PathError{Op: "open", Path: "x", Err: windows.ERROR_PATH_NOT_FOUND}, errGone},
		// A WSL symlink or FIFO, or an app execution alias: os.Root can't
		// open it (ELOOP), nor can a plain open (1920); a link loop (1921).
		{&fs.PathError{Op: "openat", Path: "x", Err: syscall.ELOOP}, errGone},
		{&fs.PathError{Op: "open", Path: "x", Err: windows.ERROR_CANT_ACCESS_FILE}, errGone},
		{windows.ERROR_CANT_RESOLVE_FILENAME, errGone},
		{windows.ERROR_ACCESS_DENIED, errDenied},
		{windows.ERROR_SHARING_VIOLATION, errBusy},
		{windows.ERROR_DISK_FULL, errFull},
		{windows.ERROR_FILE_TOO_LARGE, errLarge},
		{windows.ERROR_FILENAME_EXCED_RANGE, errLongName},
		{windows.ERROR_INVALID_FUNCTION, errOther},
	} {
		if got := classify(c.err); got != c.want {
			t.Errorf("classify(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

// A symlink WSL wrote is a file to the listing (ModeIrregular) that Windows
// can't open: it answers like a missing file, not 500.
func TestWSLSymlinkAnswersMissing(t *testing.T) {
	if _, err := exec.LookPath("wsl"); err != nil {
		t.Skip("no WSL")
	}
	dir := makeShare(t, map[string]string{"target.txt": "t"})
	t.Chdir(dir)
	if out, err := exec.Command("wsl", "/bin/ln", "-s", "target.txt", "link.txt").CombinedOutput(); err != nil {
		t.Skipf("WSL can't make a link here: %v: %s", err, out)
	}
	if fi, err := os.Lstat("link.txt"); err != nil || fi.Mode()&fs.ModeSymlink != 0 {
		t.Skipf("WSL made an NT symlink (or none): %v", err)
	}
	s := newServer(t, Options{Path: dir})
	for _, m := range []string{"GET", "HEAD"} {
		if w := do(s, request(m, "/link.txt", nil)); w.Code != http.StatusNotFound {
			t.Errorf("%s /link.txt = %d, want 404", m, w.Code)
		}
	}
}
