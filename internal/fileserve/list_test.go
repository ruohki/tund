package fileserve

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestListingFormats(t *testing.T) {
	dir := makeShare(t, map[string]string{"sub/": "", "a.txt": "aaa", "b.png": "png"})
	s := newServer(t, Options{Path: dir, Upload: true})

	w := get(s, "/", "text/html,application/xhtml+xml,*/*;q=0.8")
	h := w.Header()
	if w.Code != http.StatusOK || h.Get("Content-Type") != "text/html; charset=utf-8" || h.Get("Cache-Control") != "no-store" ||
		!slices.Contains(h.Values("Vary"), "Accept") || h.Get("Cross-Origin-Opener-Policy") != "same-origin" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "script-src 'nonce-") {
		t.Errorf("HTML listing: %d %v", w.Code, h)
	}

	for _, r := range []*http.Request{request("GET", "/", nil), request("GET", "/?format=json", nil)} {
		if r.URL.RawQuery == "" {
			r.Header.Set("Accept", "application/json")
		} else {
			r.Header.Set("Accept", "text/html") // ?format=json wins
		}
		w := do(s, r)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
			w.Header().Get("Content-Security-Policy") != dataCSP || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("JSON listing: %d %v", w.Code, w.Header())
		}
		var l map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		entries := l["entries"].([]any)
		if l["share"] != "share" || l["path"] != "/" || l["upload"] != true || l["total"] != 3.0 || l["truncated"] != false || len(entries) != 3 {
			t.Errorf("JSON listing = %s", w.Body)
		}
		first := entries[0].(map[string]any)
		if first["name"] != "sub" || first["href"] != "/sub/" || first["type"] != "folder" || first["kind"] != "folder" || first["size"] != nil {
			t.Errorf("folder entry = %v", first)
		}
		file := entries[1].(map[string]any)
		if file["name"] != "a.txt" || file["href"] != "/a.txt" || file["type"] != "file" || file["kind"] != "doc" || file["size"] != 3.0 {
			t.Errorf("file entry = %v", file)
		}
		if mod, err := time.Parse(time.RFC3339, file["modified"].(string)); err != nil || !strings.HasSuffix(file["modified"].(string), "Z") ||
			time.Since(mod) > time.Hour {
			t.Errorf("modified = %v", file["modified"])
		}
	}

	for _, accept := range []string{"", "*/*", "text/plain"} {
		w := get(s, "/", accept)
		if w.Code != http.StatusOK || w.Body.String() != "sub/\na.txt\nb.png\n" ||
			w.Header().Get("Content-Type") != "text/plain; charset=utf-8" || w.Header().Get("Content-Security-Policy") != dataCSP {
			t.Errorf("text listing (%q) = %d %q %v", accept, w.Code, w.Body, w.Header())
		}
	}
	if w := do(s, request("HEAD", "/", nil)); w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Errorf("HEAD / = %d %q", w.Code, w.Body)
	}
	if w := get(s, "/sub/", ""); w.Code != http.StatusOK || w.Body.String() != "" {
		t.Errorf("empty folder = %d %q", w.Code, w.Body)
	}
	if w := get(s, "/sub/", "application/json"); !strings.Contains(w.Body.String(), `"entries":[]`) {
		t.Errorf("empty JSON folder = %s", w.Body)
	}
}

// TestFolderRedirectKeepsQuery: the slash redirect keeps ?format=json and
// ?sort=, and isn't cached, as the folder may become a file.
func TestFolderRedirectKeepsQuery(t *testing.T) {
	s := newServer(t, Options{Path: makeShare(t, map[string]string{"sub/b.txt": "b"})})
	for target, want := range map[string]string{
		"/sub":                      "/sub/",
		"/sub?format=json":          "/sub/?format=json",
		"/sub?sort=size&order=desc": "/sub/?sort=size&order=desc",
	} {
		for _, m := range []string{"GET", "HEAD"} {
			w := do(s, request(m, target, nil))
			if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != want || w.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("%s %s = %d %q %q, want 301 %q", m, target, w.Code, w.Header().Get("Location"), w.Header().Get("Cache-Control"), want)
			}
			checkSafetyHeaders(t, w.Result())
		}
	}
	if names := listNames(t, s, "/sub/?format=json"); !slices.Equal(names, []string{"b.txt"}) {
		t.Errorf("after the redirect: %q", names)
	}
}

// TestCurlLabels: curl -OJ keeps a name only when it is plain ASCII, so the
// snippets promise that only then.
func TestCurlLabels(t *testing.T) {
	dir := makeShare(t, map[string]string{"plain/a.txt": "a", "plain/z/": "", "other/Café.txt": "c"})
	s := newServer(t, Options{Path: dir})
	for target, want := range map[string]string{
		"/plain/": "download a file, keeping its name",
		"/other/": "download a file (curl saves letters outside ASCII as _)",
	} {
		if html := get(s, target, "text/html").Body.String(); !strings.Contains(html, want) {
			t.Errorf("%s lacks %q", target, want)
		}
	}
	for path, want := range map[string]string{
		"plain/a.txt":    "download the file, keeping its name",
		"other/Café.txt": "download the file (curl saves letters outside ASCII as _)",
	} {
		one := newServer(t, Options{Path: filepath.Join(dir, filepath.FromSlash(path))})
		if html := get(one, "/", "text/html").Body.String(); !strings.Contains(html, want) {
			t.Errorf("the page of %s lacks %q", path, want)
		}
	}
}

func TestListingOrder(t *testing.T) {
	files := map[string]string{"z/": "", "B/": "", "a10/": "", "a2/": "",
		"img10.png": "1234567", "img2.png": "1", "Img1.png": "12", "b.txt": "123", "a.txt": "1234", "a01.txt": "12345", "a1.txt": "123456"}
	dir := makeShare(t, files)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, n := range []string{"img10.png", "img2.png", "Img1.png", "b.txt", "a.txt", "a01.txt", "a1.txt", "z", "B", "a10", "a2"} {
		os.Chtimes(filepath.Join(dir, n), base, base.Add(time.Duration(i)*time.Hour))
	}
	s := newServer(t, Options{Path: dir})
	for q, want := range map[string]string{
		"":                           "a2/ a10/ B/ z/ a.txt a1.txt a01.txt b.txt Img1.png img2.png img10.png",
		"?sort=name&order=desc":      "z/ B/ a10/ a2/ img10.png img2.png Img1.png b.txt a01.txt a1.txt a.txt",
		"?sort=size":                 "a2/ a10/ B/ z/ img2.png Img1.png b.txt a.txt a01.txt a1.txt img10.png",
		"?sort=size&order=desc":      "z/ B/ a10/ a2/ img10.png a1.txt a01.txt a.txt b.txt Img1.png img2.png",
		"?sort=modified":             "z/ B/ a10/ a2/ img10.png img2.png Img1.png b.txt a.txt a01.txt a1.txt",
		"?sort=modified&order=desc":  "a2/ a10/ B/ z/ a1.txt a01.txt a.txt b.txt Img1.png img2.png img10.png",
		"?sort=bogus&order=sideways": "a2/ a10/ B/ z/ a.txt a1.txt a01.txt b.txt Img1.png img2.png img10.png",
	} {
		w := get(s, "/"+q, "")
		if got := strings.Join(strings.Fields(w.Body.String()), " "); got != want {
			t.Errorf("GET /%s =\n%s\nwant\n%s", q, got, want)
		}
	}
}

func TestNatural(t *testing.T) {
	sorted := []string{"", "1", "01", "2", "10", "a", "a1", "a01", "a2", "a10", "a10b", "a10c", "ab", "img2", "img10", "img10a", "x99999999999999999999", "x100000000000000000000"}
	for i := range sorted {
		for j := range sorted {
			if got, want := natural(sorted[i], sorted[j]), cmpInt(i, j); got != want {
				t.Errorf("natural(%q, %q) = %d, want %d", sorted[i], sorted[j], got, want)
			}
		}
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func TestListingHidesNames(t *testing.T) {
	dir := makeShare(t, map[string]string{"ok.txt": "ok", ".env": "S=1", ".DS_Store": "x", "Thumbs.db": "x", "desktop.ini": "x",
		"id_rsa": "x", "secrets/": "", ".git/": "", "lost+found/": "", tempPrefix + "0123": "partial", "wp-config.php": "x"})
	var odd []string
	for _, n := range []string{"a\nb.txt", "esc\x1b[31m.txt", "tab\t.txt", "Icon\r", "del\x7f.txt", "back\\slash.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err == nil {
			odd = append(odd, n)
		}
	}
	s := newServer(t, Options{Path: dir})
	if w := get(s, "/", ""); w.Body.String() != "ok.txt\n" {
		t.Errorf("text listing = %q", w.Body)
	}
	if names := listNames(t, s, "/"); !slices.Equal(names, []string{"ok.txt"}) {
		t.Errorf("JSON listing = %q", names)
	}
	html := get(s, "/", "text/html").Body.String()
	for _, n := range append(odd, ".env", "Thumbs.db", "id_rsa", "secrets", tempPrefix, "wp-config") {
		if strings.Contains(html, n) {
			t.Errorf("HTML listing shows %q", n)
		}
	}
	for _, n := range append(odd, ".env", ".DS_Store", "Thumbs.db", "desktop.ini", "id_rsa", tempPrefix+"0123", "wp-config.php") {
		if w := get(s, href([]string{n}, false), ""); w.Code != http.StatusNotFound {
			t.Errorf("GET %q = %d", n, w.Code)
		}
	}
	for _, n := range []string{"secrets", ".git", "lost+found"} {
		if w := get(s, href([]string{n}, true), ""); w.Code != http.StatusNotFound {
			t.Errorf("GET %q/ = %d", n, w.Code)
		}
	}
}

func TestListingHrefs(t *testing.T) {
	names := map[string]string{
		"a:b":                     "/a:b",
		"javascript:alert(1)":     "/javascript:alert%281%29",
		"q?x":                     "/q%3Fx",
		"h#sh":                    "/h%23sh",
		"pct%41":                  "/pct%2541",
		"semi;colon":              "/semi%3Bcolon",
		"a b":                     "/a%20b",
		"\u00FC.txt":              "/%C3%BC.txt",
		"it's":                    "/it%27s",
		`"><img src=x onerror=1>`: "/%22%3E%3Cimg%20src=x%20onerror=1%3E",
		"<b>bold<":                "/%3Cb%3Ebold%3C",
		"x\u202Egnp.exe":          "/x%E2%80%AEgnp.exe",
	}
	files := map[string]string{"dir:x/": ""}
	dir := makeShare(t, files)
	made := map[string]string{"dir:x": "/dir:x/"}
	for n, h := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644); err == nil {
			made[n] = h
		}
	}
	s := newServer(t, Options{Path: dir})
	w := get(s, "/", "application/json")
	var l jsonListing
	json.Unmarshal(w.Body.Bytes(), &l)
	got := map[string]string{}
	for _, e := range l.Entries {
		got[e.Name] = e.Href
	}
	for n, h := range made {
		if got[n] != h {
			t.Errorf("href of %q = %q, want %q", n, got[n], h)
		}
		if strings.HasSuffix(h, "/") {
			continue
		}
		r := request("GET", "", nil)
		r.URL, _ = r.URL.Parse(h)
		if w := do(s, r); w.Code != http.StatusOK || w.Body.String() != n {
			t.Errorf("GET %s = %d %q", h, w.Code, w.Body)
		}
	}
	html := get(s, "/", "text/html").Body.String()
	if strings.Contains(html, "ZgotmplZ") || strings.Contains(html, "<img src=x") || strings.Contains(html, "<b>bold") ||
		strings.Contains(html, "\u202E") {
		t.Errorf("unsafe HTML listing:\n%s", html)
	}
	for _, h := range made {
		if !strings.Contains(html, `href="`+h+`"`) {
			t.Errorf("HTML listing lacks href %q", h)
		}
	}
	if _, ok := made["x\u202Egnp.exe"]; ok && !strings.Contains(html, `x\u202Egnp.exe`) {
		t.Error("the bidi override isn't shown as an escape")
	}
}

func TestListingTruncated(t *testing.T) {
	oldRows, oldScan := maxRows, maxScan
	t.Cleanup(func() { maxRows, maxScan = oldRows, oldScan })
	maxRows = 3
	files := map[string]string{}
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		files[n+".txt"] = n
	}
	s := newServer(t, Options{Path: makeShare(t, files)})
	html := get(s, "/", "text/html").Body.String()
	if !strings.Contains(html, "Showing the first 3 of 5 items.") || strings.Count(html, `<tr class="k-`) != 3 {
		t.Errorf("truncated HTML:\n%s", html)
	}
	var l jsonListing
	json.Unmarshal(get(s, "/", "application/json").Body.Bytes(), &l)
	if l.Total != 5 || l.Truncated || len(l.Entries) != 5 {
		t.Errorf("JSON = %+v", l)
	}
	maxScan = 4
	json.Unmarshal(get(s, "/", "application/json").Body.Bytes(), &l)
	if l.Total != 4 || !l.Truncated || len(l.Entries) != 4 {
		t.Errorf("capped JSON = %+v", l)
	}
	if html := get(s, "/", "text/html").Body.String(); !strings.Contains(html, "Showing the first 3 of more than 4 items.") {
		t.Errorf("capped HTML:\n%s", html)
	}
}

// keyPEM returns a freshly generated private key in PEM form.
func keyPEM(t *testing.T) string {
	t.Helper()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

const certPEM = "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ2VydGlmaWNhdGVPbmx5\n-----END CERTIFICATE-----\n"

func TestListingSniffsKeys(t *testing.T) {
	key := keyPEM(t)
	dir := makeShare(t, map[string]string{"server.pem": key, "server.key": key, "cert.pem": certPEM, "notes.txt": "see:\n" + key,
		"Deck.key": "PK\x03\x04keynote", "readme.txt": "mentions BEGIN PRIVATE KEY in prose"})
	s := newServer(t, Options{Path: dir})
	want := []string{"cert.pem", "Deck.key", "notes.txt", "readme.txt"}
	if names := listNames(t, s, "/"); !slices.Equal(names, want) {
		t.Errorf("listing = %q, want %q", names, want)
	}
	// The verdict is cached, and a changed file is looked at again.
	if names := listNames(t, s, "/"); !slices.Equal(names, want) {
		t.Errorf("second listing = %q", names)
	}
	os.WriteFile(filepath.Join(dir, "cert.pem"), []byte(certPEM+key), 0o644)
	later := time.Now().Add(time.Minute)
	os.Chtimes(filepath.Join(dir, "cert.pem"), later, later)
	if names := listNames(t, s, "/"); slices.Contains(names, "cert.pem") {
		t.Errorf("listing after a key was added = %q", names)
	}
}

func TestListingPage(t *testing.T) {
	dir := makeShare(t, map[string]string{"photos/2026/img.png": "png", "photos/a.txt": "a"})
	s := newServer(t, Options{Path: dir, Upload: true})
	r := request("GET", "/photos/", nil)
	r.Header.Set("Accept", "text/html")
	r.Header.Set("X-Forwarded-Proto", "https")
	html := do(s, r).Body.String()
	for _, want := range []string{
		`<title>Shared folder</title>`,
		`<h1 class="eyebrow"><a href="/">Shared folder</a></h1>`,
		`<a href="/" dir="auto">share</a>`,
		`<span aria-current="page" dir="auto">photos</span>`,
		`<p class="title" dir="auto">photos</p>`,
		`<p class="meta">1 folder · 1 file</p>`,
		`href="/photos/2026/"`, `href="/photos/a.txt"`, `href="/photos/a.txt?download"`,
		`aria-label="Download a.txt"`,
		`<form class="upload" method="post" action="/photos/" enctype="multipart/form-data">`,
		`Signed in with password`, `href="/_tund/auth/logout"`,
		`curl -u share &#39;https://share.example/photos/&#39;`,
		`curl -u share -OJ &#39;https://share.example/photos/a.txt&#39;`,
		`curl -u share -T file &#39;https://share.example/photos/&#39;`,
		`Served directly from the owner's computer · <span dir="auto">share.example</span>`,
		`aria-sort="ascending"><a href="/photos/?sort=name&amp;order=desc">Name</a>`,
		`<a href="/photos/?sort=size&amp;order=asc">Size</a>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("listing page lacks %s", want)
		}
	}

	r = request("GET", "/photos/", nil)
	r.Header.Set("Accept", "text/html")
	r.Header.Set("X-Tund-Auth", "oidc")
	r.Header.Set("X-Tund-User-Email", "alice@example.com")
	html = do(s, r).Body.String()
	if !strings.Contains(html, `title="Signed in as alice@example.com">alice@example.com</span>`) || strings.Contains(html, "Use curl") {
		t.Errorf("OIDC listing page:\n%s", html)
	}
	r.Header.Del("X-Tund-User-Email")
	if html := do(s, r).Body.String(); !strings.Contains(html, ">Signed in</span>") {
		t.Errorf("anonymous OIDC chip missing")
	}

	ro := newServer(t, Options{Path: dir})
	if html := get(ro, "/", "text/html").Body.String(); strings.Contains(html, "<form") || strings.Contains(html, "-T file") {
		t.Errorf("read-only page offers uploads:\n%s", html)
	}
}

func TestPageHelpers(t *testing.T) {
	for _, c := range []struct {
		folders, files int
		want           string
	}{{0, 0, "Empty folder"}, {1, 0, "1 folder"}, {0, 1, "1 file"}, {3, 1200, "3 folders · 1,200 files"}} {
		if got := meta(c.folders, c.files); got != c.want {
			t.Errorf("meta(%d, %d) = %q", c.folders, c.files, got)
		}
	}
	for _, c := range []struct {
		n, k int
		want string
	}{
		{1, 0, "1 file uploaded."},
		{1, 1, "1 file uploaded. 1 was renamed because a file with the same name exists."},
		{3, 2, "3 files uploaded. 2 were renamed because files with the same names exist."},
	} {
		if got := flash(c.n, c.k); got != c.want {
			t.Errorf("flash(%d, %d) = %q", c.n, c.k, got)
		}
	}
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 2516582: "2.4 MB", 1 << 40: "1.0 TB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q", n, got)
		}
	}
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 123456789: "123,456,789"} {
		if got := count(n); got != want {
			t.Errorf("count(%d) = %q", n, got)
		}
	}
	for name, want := range map[string]string{
		"a.PNG": "image", "x.svg": "image", "clip.MOV": "video", "song.flac": "audio", "doc.pdf": "pdf", "notes.md": "doc",
		"t.xlsx": "sheet", "Deck.key": "slides", "a.tar.gz": "archive", "b.tgz": "archive", "main.go": "code",
		"Dockerfile": "code", "makefile": "code", "id.pub": "cert", "README": "other", "x.weird": "other",
	} {
		if got := kindOf(name, false); got != want {
			t.Errorf("kindOf(%q) = %q, want %q", name, got, want)
		}
	}
	if kindOf("photos.png", true) != "folder" {
		t.Error("folders are folders")
	}
	for name, want := range map[string]string{
		"a.png": "PNG image", "r.pdf": "PDF document", "n.md": "MD document", "t.csv": "CSV spreadsheet",
		"d.pptx": "PPTX presentation", "a.tar.gz": "TAR.GZ archive", "x.go": "GO file", "README": "File", "x.weird": "WEIRD file",
	} {
		if got := kindLabel(name); got != want {
			t.Errorf("kindLabel(%q) = %q, want %q", name, got, want)
		}
	}
}
