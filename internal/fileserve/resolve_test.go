package fileserve

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// symlink makes a link or skips the test where links need privileges.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestExactNames(t *testing.T) {
	dir := makeShare(t, map[string]string{"README.md": "readme", "id_rsa": "KEY", ".ssh/config": "Host x", "sub/a.txt": "a"})
	s := newServer(t, Options{Path: dir})
	if caseInsensitive(t, dir) {
		// What the OS would do with the visitor's spelling: that's why only
		// names from directory listings reach the file system.
		if _, err := os.Stat(filepath.Join(dir, "ID_RSA")); err != nil {
			t.Errorf("a case-insensitive file system should open ID_RSA: %v", err)
		}
	}
	for _, p := range []string{"/README.MD", "/readme.md", "/ID_RSA", "/id_rsa", "/Id_Rsa", "/.SSH/config", "/.ssh/config",
		"/SUB/a.txt", "/sub/A.TXT", "/Sub/", "/README.md/", "/sub/a.txt/x"} {
		if w := get(s, p, ""); w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, w.Code)
		}
	}
	if w := get(s, "/README.md", ""); w.Code != http.StatusOK || w.Body.String() != "readme" {
		t.Errorf("GET /README.md = %d %q", w.Code, w.Body)
	}
}

func TestNFCFallback(t *testing.T) {
	nfd, nfc := "cafe\u0301.txt", "caf\u00E9.txt"
	dir := makeShare(t, map[string]string{nfd: "nfd", "U\u0308bung/x.txt": "x"})
	s := newServer(t, Options{Path: dir})
	for _, p := range []string{"/cafe%CC%81.txt", "/caf%C3%A9.txt"} {
		w := get(s, p, "")
		if w.Code != http.StatusOK || w.Body.String() != "nfd" || w.Header().Get("Location") != "" {
			t.Errorf("GET %s = %d %q, want the NFD file served directly", p, w.Code, w.Body)
		}
	}
	if w := get(s, "/%C3%9Cbung/", ""); w.Code != http.StatusOK || w.Body.String() != "x.txt\n" {
		t.Errorf("NFC folder = %d %q", w.Code, w.Body)
	}
	if w := get(s, "/%C3%9Cbung", ""); w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/U%CC%88bung/" {
		t.Errorf("NFC folder redirect = %d %q", w.Code, w.Header().Get("Location"))
	}
	if names := listNames(t, s, "/"); !slices.Contains(names, nfd) {
		t.Errorf("listing = %q, want the real (NFD) name", names)
	}

	// Where the file system keeps both forms apart, each URL gets its own
	// file, and a third spelling that matches both is ambiguous.
	f, err := os.OpenFile(filepath.Join(dir, nfc), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Logf("both forms can't coexist here: %v", err)
		return
	}
	f.WriteString("nfc")
	f.Close()
	for p, want := range map[string]string{"/cafe%CC%81.txt": "nfd", "/caf%C3%A9.txt": "nfc"} {
		if w := get(s, p, ""); w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("GET %s = %d %q, want %q", p, w.Code, w.Body, want)
		}
	}
	os.WriteFile(filepath.Join(dir, "\u00C5.txt"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(dir, "A\u030A.txt"), []byte("2"), 0o644)
	if w := get(s, "/%E2%84%AB.txt", ""); w.Code != http.StatusNotFound { // U+212B ANGSTROM SIGN
		t.Errorf("ambiguous NFC match = %d", w.Code)
	}
}

func TestSymlinks(t *testing.T) {
	dir := makeShare(t, map[string]string{
		".env": "SECRET=1", ".config/gh/hosts.yml": "token: x", ".git/config": "[core]",
		"sub/readme.txt": "readme", "plain.txt": "plain", "keys/": "",
	})
	os.WriteFile(filepath.Join(filepath.Dir(dir), "outside.txt"), []byte("outside"), 0o644)
	link := func(target, name string) { symlink(t, target, filepath.Join(dir, filepath.FromSlash(name))) }
	link(".env", "notes.txt")
	link(".config", "config")
	link(".git", "repo")
	link(filepath.Join(dir, "sub", "readme.txt"), "abs")
	link("../outside.txt", "out")
	link("loop2", "loop1")
	link("loop1", "loop2")
	link("self", "self")
	for i := 1; i <= 9; i++ {
		target := "chain" + string(rune('0'+i+1))
		if i == 9 {
			target = "sub/readme.txt"
		}
		link(target, "chain"+string(rune('0'+i)))
	}
	for i := 1; i <= 8; i++ {
		target := "ok" + string(rune('0'+i+1))
		if i == 8 {
			target = "sub/readme.txt"
		}
		link(target, "ok"+string(rune('0'+i)))
	}
	link("sub", "docs")
	link("sub/readme.txt", "readme-link.txt")
	link("sub/readme.txt", "id_rsa")
	link("sub", "secrets")
	link("nope", "dangling")
	link("..", "sub/up")
	link("../sub/readme.txt", "sub/back.txt")
	link("../.git/config", "sub/deep")
	link("../.config/gh", "sub/gh")
	link("./../sub/./readme.txt", "sub/dots.txt")
	link("../keys", "sub/k")
	s := newServer(t, Options{Path: dir})

	listed := listNames(t, s, "/")
	want := []string{"docs", "keys", "sub", "chain2", "chain3", "chain4", "chain5", "chain6", "chain7", "chain8", "chain9",
		"ok1", "ok2", "ok3", "ok4", "ok5", "ok6", "ok7", "ok8", "plain.txt", "readme-link.txt"}
	if !slices.Equal(listed, want) {
		t.Errorf("root listing = %q\nwant %q", listed, want)
	}
	if listed := listNames(t, s, "/sub/"); !slices.Equal(listed, []string{"k", "back.txt", "dots.txt", "readme.txt"}) {
		t.Errorf("sub listing = %q", listed)
	}
	for p, want := range map[string]string{
		"/ok1": "readme", "/chain2": "readme", "/docs/readme.txt": "readme", "/readme-link.txt": "readme",
		"/sub/back.txt": "readme", "/sub/dots.txt": "readme", "/docs/back.txt": "readme",
	} {
		if w := get(s, p, ""); w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("GET %s = %d %q, want %q", p, w.Code, w.Body, want)
		}
	}
	if w := get(s, "/docs/", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "readme.txt\n") {
		t.Errorf("GET /docs/ = %d %q", w.Code, w.Body)
	}
	for _, p := range []string{"/notes.txt", "/config/", "/config/gh/hosts.yml", "/repo/", "/repo/config", "/abs", "/out",
		"/loop1", "/self", "/chain1", "/id_rsa", "/secrets/", "/secrets/readme.txt", "/dangling", "/sub/up/", "/sub/up/.env",
		"/sub/deep", "/sub/gh/hosts.yml", "/docs/up/"} {
		w := get(s, p, "")
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, w.Code)
		}
		if strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "token") {
			t.Errorf("GET %s leaked %q", p, w.Body)
		}
	}
}

func TestSymlinkUploads(t *testing.T) {
	dir := makeShare(t, map[string]string{"sub/": "", ".git/": ""})
	symlink(t, "sub", filepath.Join(dir, "docs"))
	symlink(t, ".git", filepath.Join(dir, "repo"))
	var rec recorder
	s := newServer(t, Options{Path: dir, Upload: true, OnUpload: rec.add})
	w := do(s, request("PUT", "/docs/a.txt", strings.NewReader("a")))
	if w.Code != http.StatusCreated || w.Header().Get("Location") != "/docs/a.txt" {
		t.Errorf("PUT through a link = %d %q", w.Code, w.Header().Get("Location"))
	}
	if readFile(t, filepath.Join(dir, "sub", "a.txt")) != "a" {
		t.Error("the upload isn't in the link's target")
	}
	if ups := rec.all(); len(ups) != 1 || ups[0].Path != "/sub/a.txt" {
		t.Errorf("OnUpload = %+v, want the real path", ups)
	}
	if w := do(s, request("PUT", "/repo/hooks", strings.NewReader("x"))); w.Code != http.StatusNotFound {
		t.Errorf("PUT into a link to .git = %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("upload landed in .git: %v", err)
	}
}

// TestSwappedFile: a file that turns into a link to a secret after it was
// listed is refused, not followed.
func TestSwappedFile(t *testing.T) {
	dir := makeShare(t, map[string]string{".env": "SECRET=1", "notes.txt": "notes"})
	s := newServer(t, Options{Path: dir})
	if names := listNames(t, s, "/"); !slices.Equal(names, []string{"notes.txt"}) {
		t.Fatalf("listing = %q", names)
	}
	os.Remove(filepath.Join(dir, "notes.txt"))
	symlink(t, ".env", filepath.Join(dir, "notes.txt"))
	if w := get(s, "/notes.txt", ""); w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "SECRET") {
		t.Errorf("GET swapped file = %d %q", w.Code, w.Body)
	}
	// Between resolving and opening, only the entry that was resolved is served.
	info := mustLstat(t, filepath.Join(dir, ".env"))
	if _, _, err := s.open(node{segs: []string{"x"}, rel: ".env", info: info}); err != nil {
		t.Fatalf("open .env with its own info: %v", err)
	}
	if _, _, err := s.open(node{segs: []string{"x"}, rel: ".env", info: mustLstat(t, filepath.Join(dir, "notes.txt"))}); !errors.Is(err, errHidden) {
		t.Errorf("open with another file's info = %v, want errHidden", err)
	}
}

// TestSwappedAfterResolving: an entry swapped for a link that leaves the
// share between resolving and opening answers 404, never 500.
func TestSwappedAfterResolving(t *testing.T) {
	dir := makeShare(t, map[string]string{"a.txt": "a", "sub/b.txt": "b"})
	os.WriteFile(filepath.Join(filepath.Dir(dir), "outside.txt"), []byte("outside"), 0o644)
	s := newServer(t, Options{Path: dir})
	file, _, err := s.resolver().resolve([]string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	folder, _, err := s.resolver().resolve([]string{"sub"})
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "a.txt"))
	symlink(t, "../outside.txt", filepath.Join(dir, "a.txt"))
	os.RemoveAll(filepath.Join(dir, "sub"))
	symlink(t, "..", filepath.Join(dir, "sub"))

	w := httptest.NewRecorder()
	s.download(w, request("GET", "/a.txt", nil), file, false)
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "outside") {
		t.Errorf("download after the swap = %d %q", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	s.listing(w, request("GET", "/sub/", nil), s.resolver(), folder)
	if w.Code != http.StatusNotFound {
		t.Errorf("listing after the swap = %d %q", w.Code, w.Body)
	}
}

func mustLstat(t *testing.T, p string) fs.FileInfo {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func TestVisibleName(t *testing.T) {
	for name, want := range map[string]bool{
		"report.pdf": true, "Deck.key": true, "id_ed25519.pub": true, ".env.example": false, ".DS_Store": false,
		"Thumbs.db": false, "THUMBS.DB": false, "desktop.ini": false, "$RECYCLE.BIN": false, "lost+found": false,
		"System Volume Information": false, "a\\b": false, "a\nb": false, "a\x7fb": false, "Icon\r": false,
		"id_rsa": false, "ID_RSA": false, "credentials.json": false, "": false, "tab\tname": false,
	} {
		if got := visibleName(name, false); got != want {
			t.Errorf("visibleName(%q) = %v, want %v", name, got, want)
		}
	}
	for name, want := range map[string]bool{".git": false, "secrets": false, ".SSH": false, "docs": true, "Deck.key": true} {
		if got := visibleName(name, true); got != want {
			t.Errorf("visibleName(%q, dir) = %v, want %v", name, got, want)
		}
	}
}

func TestScanCap(t *testing.T) {
	old := maxScan
	maxScan = 3
	t.Cleanup(func() { maxScan = old })
	files := map[string]string{}
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		files[n+".txt"] = n
	}
	s := newServer(t, Options{Path: makeShare(t, files)})
	d, err := s.resolver().list(".")
	if err != nil || len(d.names) != 3 || !d.capped {
		t.Fatalf("list = %+v, %v", d, err)
	}
	found := 0
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		if get(s, "/"+n+".txt", "").Code == http.StatusOK {
			found++
		}
	}
	if found != 3 {
		t.Errorf("%d files reachable past a scan cap of 3", found)
	}
}
