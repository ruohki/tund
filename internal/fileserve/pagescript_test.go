package fileserve

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPageScripts runs a folder page's own script in node, against a small
// DOM built from the page (testdata/pagescript.mjs), through what visitors
// do: uploads that are saved, renamed, canceled or refused with 401, errors,
// and keyboard focus while the rows reload. It is skipped without node.
func TestPageScripts(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := makeShare(t, map[string]string{"inbox/a.txt": "a", "inbox/b.txt": "b", "inbox/\u05f3invoice.pdf\u05f3.exe": "x"})
	s := newServer(t, Options{Path: dir, Upload: true})
	tmp := t.TempDir()
	page, listing := filepath.Join(tmp, "page.html"), filepath.Join(tmp, "listing.json")
	write := func(name string, b []byte) {
		if err := os.WriteFile(name, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(page, get(s, "/inbox/", "text/html").Body.Bytes())
	// The rows reload from a listing with one file more.
	write(filepath.Join(dir, "inbox", "c.txt"), []byte("c"))
	write(listing, get(s, "/inbox/?format=json", "").Body.Bytes())
	for _, scenario := range []string{"name boxes", "cancel while saving", "session ended", "renamed", "error boundary", "focus"} {
		t.Run(scenario, func(t *testing.T) {
			out, err := exec.Command(node, filepath.Join("testdata", "pagescript.mjs"), page, listing, scenario).CombinedOutput()
			if err != nil {
				t.Errorf("%v\n%s", err, out)
			}
		})
	}
}
