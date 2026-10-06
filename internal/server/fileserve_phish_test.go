package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"tund/internal/fileserve"
)

// The pages of tund serve (internal/fileserve) reach maybeScan like any HTML
// the edge proxies, so their own copy must score nothing, and visitors' file
// and folder names must stay out of <title>, <h1> and <h2>, which score the
// most. Names in the listing rows still count: a folder of files named after
// brands and phishing phrases can reach the threshold on its rows alone. That
// is a known limitation of v1 (the fix is on the edge: skip the scan for file
// shares), so TestFileSharePhishRows only checks where those names land.

const fileShareHost = "files.example"

// fileShare builds a share folder under a home folder of its own (CheckRoot
// refuses the real one's surroundings) and serves it.
func fileShare(t *testing.T, root string, files map[string]string, opts fileserve.Options) *fileserve.Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(t.TempDir(), root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts.Path = filepath.Join(dir, filepath.FromSlash(opts.Path)) // "" is the folder itself
	s, err := fileserve.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	return s
}

// fileSharePage requests target the way the edge forwards a browser's
// request: signed in with the password, from one visitor address.
func fileSharePage(s http.Handler, method, target string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://"+fileShareHost+target, strings.NewReader("x"))
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	r.Header.Set("X-Tund-Auth", "password")
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	for k, v := range header {
		r.Header.Set(k, v)
	}
	if header["X-Test-Size"] == "huge" {
		r.ContentLength = 1 << 62 // more than any disk has free: refused before the body is read
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func privateKeyPEM(t *testing.T) string {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestFileSharePagesPassPhishHeuristic(t *testing.T) {
	files := map[string]string{"drafts/": "", "empty/": "", "inbox/": "", "locked/a.txt": "a",
		"hero@2x.png": "png", "budget.xlsx": "xlsx", "notes.txt": "notes", "teaser-cut-v3.mp4": "mp4",
		"key.pem": privateKeyPEM(t), "keys.txt": "deploy key:\n" + privateKeyPEM(t)}
	rw := fileShare(t, "Q3 assets", files, fileserve.Options{Upload: true})
	ro := fileShare(t, "Q3 assets", files, fileserve.Options{})
	one := fileShare(t, "Q3 assets", files, fileserve.Options{Path: "notes.txt"})
	closed := fileShare(t, "Q3 assets", files, fileserve.Options{})
	closed.Close(context.Background())
	oidc := map[string]string{"X-Tund-Auth": "oidc", "X-Tund-User-Email": "alice@example.com"}

	cases := []struct {
		name           string
		s              http.Handler
		method, target string
		header         map[string]string
		code           int
	}{
		{"listing", rw, "GET", "/", nil, 200},
		{"listing oidc", rw, "GET", "/", oidc, 200},
		{"listing read-only", ro, "GET", "/", nil, 200},
		{"empty listing", rw, "GET", "/empty/", nil, 200},
		{"empty read-only listing", ro, "GET", "/empty/", oidc, 200},
		{"after a form upload", rw, "GET", "/drafts/?uploaded=2&renamed=1", nil, 200},
		{"single file", one, "GET", "/", nil, 200},
		{"single file oidc", one, "GET", "/", oidc, 200},
		{"404", rw, "GET", "/drafts/old-hero.png", nil, 404},
		{"404 single file", one, "GET", "/other.txt", nil, 404},
		{"403 not signed in", rw, "GET", "/", map[string]string{"X-Tund-Auth": "none"}, 403},
		{"403 replay", rw, "GET", "/", map[string]string{"X-Forwarded-For": "replay"}, 403},
		{"403 cross-site", rw, "GET", "/notes.txt", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, 403},
		// Listed text that holds a secret says why it isn't served (key-like
		// files such as key.pem are hidden and answer like missing ones).
		{"403 secret content", rw, "GET", "/keys.txt", nil, 403},
		{"403 dotfile upload", rw, "PUT", "/inbox/.env", nil, 403},
		{"403 refused upload", rw, "PUT", "/inbox/desktop.ini", nil, 403},
		{"405 uploads off", ro, "PUT", "/inbox/a.txt", nil, 405},
		{"405 method", rw, "DELETE", "/notes.txt", nil, 405},
		{"400 no file name", rw, "PUT", "/inbox/", nil, 400},
		{"400 bad file name", rw, "PUT", "/inbox/...", nil, 400},
		{"503 stopped", closed, "GET", "/", nil, 503},
		// What the owner's computer causes: a full disk (refused before the
		// body is read) and an unreadable folder.
		{"507 disk full", rw, "PUT", "/inbox/big.iso", map[string]string{"X-Test-Size": "huge"}, 507},
		{"403 unreadable folder", rw, "GET", "/locked/", nil, 403},
	}
	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		locked := filepath.Join(rw.Root().Path, "locked")
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
	} else {
		cases = cases[:len(cases)-1] // permissions don't lock out administrators or root
	}
	for _, c := range cases {
		w := fileSharePage(c.s, c.method, c.target, c.header)
		if w.Code != c.code && !(c.code == 507 && w.Code == 201) { // 201: this OS can't tell free space
			t.Errorf("%s: %s %s = %d, want %d", c.name, c.method, c.target, w.Code, c.code)
		}
		if ct := w.Header().Get("Content-Type"); w.Code != 201 && ct != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", c.name, ct)
			continue
		}
		if score, signals := phishScore(w.Body.String(), fileShareHost); score != 0 {
			t.Errorf("%s: phishScore = %d %v", c.name, score, signals)
		}
	}
}

var (
	reFileShareHead    = regexp.MustCompile(`(?is)<title[^>]*>.*?</title>|<h[12][^>]*>.*?</h[12]>`)
	headPhishSignals   = []string{"brand in title/heading", "password field", "form posts to"}
	bookkeepingFolder  = "Bookkeeping"
	bookkeepingEntries = []string{"Ledger 2025.xlsx", "PayPal export.csv", "card number change form.pdf", "update your payment info.docx"}
)

// TestFileSharePhishRows: a folder whose files are named like a phishing kit
// (crit-ux's "bookkeeping" folder) keeps those names out of <title>, <h1> and
// <h2>, and everything it scores comes from the rows.
func TestFileSharePhishRows(t *testing.T) {
	files := map[string]string{}
	for _, n := range bookkeepingEntries {
		files["2025/"+n] = n
	}
	rw := fileShare(t, bookkeepingFolder, files, fileserve.Options{Upload: true})
	one := fileShare(t, bookkeepingFolder, files, fileserve.Options{Path: "2025/PayPal export.csv"})
	for name, w := range map[string]*httptest.ResponseRecorder{
		"listing":     fileSharePage(rw, "GET", "/2025/", nil),
		"single file": fileSharePage(one, "GET", "/", nil),
		"404":         fileSharePage(rw, "GET", "/2025/PayPal%20export%20old.csv", nil),
	} {
		body := w.Body.String()
		for _, head := range reFileShareHead.FindAllString(body, -1) {
			for _, n := range append([]string{bookkeepingFolder, "2025"}, bookkeepingEntries...) {
				if strings.Contains(strings.ToLower(head), strings.ToLower(strings.TrimSuffix(n, filepath.Ext(n)))) {
					t.Errorf("%s: %q appears in %q", name, n, head)
				}
			}
		}
		score, signals := phishScore(body, fileShareHost)
		for _, s := range signals {
			for _, head := range headPhishSignals {
				if strings.HasPrefix(s, head) {
					t.Errorf("%s: signal %q comes from the page's chrome", name, s)
				}
			}
		}
		t.Logf("%s: phishScore %d %v (threshold %d)", name, score, signals, phishThreshold)
	}
}
