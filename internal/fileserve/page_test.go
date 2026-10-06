package fileserve

import (
	"errors"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The edge's phishing heuristics (internal/server/abuse.go) score these
// anywhere on a page; static copy must not contain them.
var (
	phishPhrases = []string{
		"verify your account", "verify your identity", "confirm your identity", "account has been suspended",
		"account has been locked", "unusual activity", "unusual sign-in", "seed phrase", "recovery phrase",
		"secret recovery", "private key", "12-word", "24-word", "credit card number", "card number", "cvv", "cvc",
		"expiration date", "social security", "update your payment", "payment information", "sign in to continue",
		"your parcel", "delivery fee", "customs fee",
	}
	phishBrands = regexp.MustCompile(`(?i)\b(paypal|microsoft|office 365|outlook|apple id|icloud|google|gmail|amazon|netflix|` +
		`facebook|instagram|whatsapp|coinbase|binance|metamask|trust wallet|ledger|sparkasse|volksbank|commerzbank|` +
		`deutsche bank|postbank|chase|wells fargo|citibank|hsbc|barclays|revolut|n26|klarna|dhl|fedex|ups|usps|dpd|` +
		`ebay|steam|roblox|discord)\b`)
	reTitle    = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	reHeading  = regexp.MustCompile(`(?s)<h[1-6][^>]*>(.*?)</h[1-6]>`)
	reTag      = regexp.MustCompile(`(?s)<(script|style)\b([^>]*)>`)
	reStyle    = regexp.MustCompile(`(?i)<[^>]*\sstyle\s*=`)
	reHandler  = regexp.MustCompile(`(?i)<[^>]*\son[a-z]+\s*=`)
	rePassword = regexp.MustCompile(`(?i)<input[^>]+type\s*=\s*["']?password`)
	reURL      = regexp.MustCompile(`\s(href|src|action)="([^"]*)"`)
	reNonce    = regexp.MustCompile(`script-src 'nonce-([^']+)'; style-src 'nonce-([^']+)'`)
	reFragment = regexp.MustCompile(`^#[a-z][a-z-]*$`)
	fixedTitle = regexp.MustCompile(`^(Shared (folder|file)|[A-Z][a-z-]*( [a-z-]+)* · Shared (folder|file))$`)
)

// renderedPages renders every kind of page of a share whose names are what
// phishing pages use.
func renderedPages(t *testing.T, rootName string, names []string) map[string]*httptest.ResponseRecorder {
	t.Helper()
	root := filepath.Join(t.TempDir(), rootName)
	files := map[string]string{"empty/": "", "ok.txt": "ok", "key.pem": keyPEM(t)}
	for _, n := range names {
		files["Q3 receipts/"+n] = n
	}
	os.MkdirAll(root, 0o755)
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			os.MkdirAll(full, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
	}
	rw := newServer(t, Options{Path: root, Upload: true})
	ro := newServer(t, Options{Path: root})
	one := newServer(t, Options{Path: filepath.Join(root, "ok.txt")})
	closed := newServer(t, Options{Path: root})
	closed.Close(t.Context())

	pages := map[string]*httptest.ResponseRecorder{}
	html := func(name string, s *Server, method, target string, header ...string) {
		r := request(method, target, strings.NewReader("x"))
		r.Header.Set("Accept", "text/html,application/xhtml+xml")
		for i := 0; i+1 < len(header); i += 2 {
			if header[i+1] == "" {
				r.Header.Del(header[i])
			} else {
				r.Header.Set(header[i], header[i+1])
			}
		}
		pages[name] = do(s, r)
	}
	html("root", rw, "GET", "/")
	html("folder", rw, "GET", "/Q3%20receipts/")
	html("folder oidc", rw, "GET", "/Q3%20receipts/", "X-Tund-Auth", "oidc", "X-Tund-User-Email", "visitor@example.com")
	html("folder read-only", ro, "GET", "/Q3%20receipts/")
	html("empty", rw, "GET", "/empty/")
	html("flash", rw, "GET", "/empty/?uploaded=3&renamed=1")
	html("file", one, "GET", "/")
	html("404", rw, "GET", "/Q3%20receipts/nope")
	html("404 file share", one, "GET", "/nope")
	html("403 auth", rw, "GET", "/", "X-Tund-Auth", "")
	html("403 replay", rw, "GET", "/", "X-Forwarded-For", "replay")
	html("403 cross-site", rw, "GET", "/", "Sec-Fetch-Site", "cross-site")
	html("403 secret", rw, "GET", "/key.pem")
	html("403 dot name", rw, "PUT", "/empty/.env")
	html("403 refused name", rw, "PUT", "/empty/desktop.ini")
	html("405 uploads off", ro, "PUT", "/empty/a.txt")
	html("405 method", rw, "DELETE", "/ok.txt")
	html("400 no name", rw, "PUT", "/empty/")
	html("400 bad name", rw, "PUT", "/empty/...")
	html("503", closed, "GET", "/")
	for name, err := range map[string]error{
		"500": errors.New("boom"), "403 read-only": fs.ErrPermission, "409": ErrNameTaken, "507": ErrDiskFull,
		"400 canceled": ErrCanceled, "503 stopped": ErrStopped,
	} {
		w := httptest.NewRecorder()
		r := request("PUT", "/empty/x.txt", nil)
		r.Header.Set("Accept", "text/html")
		p := uploadProblem(err, "x.txt")
		if name == "500" {
			p = rw.readProblem(err, "file")
		}
		rw.fail(w, r, p)
		pages[name] = w
	}
	return pages
}

func TestPagesFollowTheRules(t *testing.T) {
	share := "Amazon seller Q3"
	names := []string{"Ledger 2025.xlsx", "PayPal export.csv", "card number change form.pdf", "verify your account.txt",
		"W-9 social security.pdf", "seed phrase.txt", "Google <b>Drive</b>.txt"}
	pages := renderedPages(t, share, names)
	user := append([]string{share, "Q3 receipts", "ok.txt", "visitor@example.com", "Drive"}, names...)
	nonces := map[string]bool{}
	for name, w := range pages {
		body := w.Body.String()
		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", name, ct)
			continue
		}
		m := reNonce.FindStringSubmatch(w.Header().Get("Content-Security-Policy"))
		if m == nil || m[1] != m[2] || len(m[1]) != 32 {
			t.Errorf("%s: CSP %q", name, w.Header().Get("Content-Security-Policy"))
			continue
		}
		nonce := m[1]
		if nonces[nonce] {
			t.Errorf("%s: nonce reused", name)
		}
		nonces[nonce] = true
		titles := reTitle.FindAllStringSubmatch(body, -1)
		if len(titles) != 1 || !fixedTitle.MatchString(titles[0][1]) {
			t.Errorf("%s: titles %q", name, titles)
		}
		var headings []string
		for _, h := range reHeading.FindAllStringSubmatch(body, -1) {
			headings = append(headings, h[1])
		}
		if !strings.Contains(body, "<h1") {
			t.Errorf("%s: no h1", name)
		}
		for _, h := range append(headings, titles[0][1]) {
			for _, n := range user {
				if strings.Contains(h, n) || strings.Contains(h, strings.ReplaceAll(n, " ", "%20")) {
					t.Errorf("%s: %q in a title or heading: %q", name, n, h)
				}
			}
		}
		tags := reTag.FindAllStringSubmatch(body, -1)
		if len(tags) != 2 {
			t.Errorf("%s: %d script/style tags", name, len(tags))
		}
		for _, tag := range tags {
			if !strings.Contains(tag[2], `nonce="`+nonce+`"`) {
				t.Errorf("%s: <%s%s> lacks the nonce", name, tag[1], tag[2])
			}
		}
		if reStyle.MatchString(body) || reHandler.MatchString(body) || rePassword.MatchString(body) {
			t.Errorf("%s: style attribute, event handler or password input", name)
		}
		if strings.Contains(body, "TUNd") || strings.Contains(strings.ToLower(body), "tund.io") {
			t.Errorf("%s: product name", name)
		}
		if !strings.Contains(body, `<html lang="en"`) || strings.Contains(body, "ZgotmplZ") || strings.Contains(body, "<b>Drive") {
			t.Errorf("%s: markup problem", name)
		}
		for _, u := range reURL.FindAllStringSubmatch(body, -1) {
			v := u[2]
			// Absolute paths, fragments of the page itself (the skip link and
			// the sprite's icons) and the favicon.
			if !(strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//")) && !reFragment.MatchString(v) &&
				!strings.HasPrefix(v, "data:image/svg+xml,") {
				t.Errorf("%s: %s=%q is not an absolute path", name, u[1], v)
			}
		}
		if strings.Contains(body, `class="title"`) && !strings.Contains(body, `<p class="title" dir="auto">`) &&
			!strings.Contains(body, `<p class="title"><span class="nm" dir="ltr"><span class="base" dir="auto">`) {
			t.Errorf("%s: a name without dir=auto", name)
		}
	}
	if w := pages["403 auth"]; strings.Contains(w.Body.String(), "Signed in") {
		t.Error("a refused request's page claims a sign-in")
	}
	if !strings.Contains(pages["404"].Body.String(), `<a class="btn" href="/Q3%20receipts/" dir="auto"><svg class="i" aria-hidden="true"><use href="#i-folder-up"/></svg>Open Q3 receipts</a>`) {
		t.Errorf("404 page lacks the way back:\n%s", pages["404"].Body)
	}
	if !strings.Contains(pages["400 no name"].Body.String(), `href="/empty/" dir="auto"><svg class="i" aria-hidden="true"><use href="#i-folder"/></svg>Back to empty</a>`) {
		t.Errorf("400 page lacks the way back:\n%s", pages["400 no name"].Body)
	}
	if !strings.Contains(pages["500"].Body.String(), ">Try again</a>") {
		t.Error("500 page lacks Try again")
	}
}

// TestPagesStaticCopy: with harmless names, pages hold nothing the edge's
// phishing heuristics count.
func TestPagesStaticCopy(t *testing.T) {
	for name, w := range renderedPages(t, "files", []string{"notes.txt"}) {
		body := strings.ToLower(w.Body.String())
		for _, p := range phishPhrases {
			if strings.Contains(body, p) {
				t.Errorf("%s: phrase %q", name, p)
			}
		}
		if m := phishBrands.FindString(body); m != "" {
			t.Errorf("%s: brand word %q", name, m)
		}
	}
}

func TestPageNoncesDiffer(t *testing.T) {
	s := newServer(t, Options{Path: makeShare(t, nil)})
	a := get(s, "/", "text/html").Header().Get("Content-Security-Policy")
	b := get(s, "/", "text/html").Header().Get("Content-Security-Policy")
	if a == b || a == "" {
		t.Errorf("nonces: %q %q", a, b)
	}
}

func TestErrorBodies(t *testing.T) {
	dir := makeShare(t, map[string]string{"a/b.txt": "b"})
	s := newServer(t, Options{Path: dir})
	real, _ := filepath.EvalSymlinks(dir)
	for _, c := range []struct {
		target, accept, want string
		code                 int
	}{
		{"/a/nope", "", "There is no file or folder at /a/nope. It may have been renamed, moved or deleted.\n", 404},
		{"/a/nope", "application/json", `{"error":"There is no file or folder at /a/nope. It may have been renamed, moved or deleted."}` + "\n", 404},
		{"/a%2Fb", "", "There is no file or folder at this address.\n", 404},
		{"/a/b.txt/", "", "There is no file or folder at /a/b.txt/. It may have been renamed, moved or deleted.\n", 404},
	} {
		r := request("GET", "", nil)
		r.URL.RawPath, r.URL.Path = c.target, mustUnescape(c.target)
		r.Header.Set("Accept", c.accept)
		w := do(s, r)
		if w.Code != c.code || w.Body.String() != c.want {
			t.Errorf("GET %s (%s) = %d %q, want %q", c.target, c.accept, w.Code, w.Body, c.want)
		}
		if strings.Contains(w.Body.String(), real) || strings.Contains(w.Body.String(), dir) {
			t.Errorf("GET %s leaks the root", c.target)
		}
	}
	if w := do(s, request("HEAD", "/nope", nil)); w.Code != http.StatusNotFound || w.Body.Len() != 0 {
		t.Errorf("HEAD /nope = %d %q", w.Code, w.Body)
	}
}

var (
	reStyleBlock  = regexp.MustCompile(`(?s)<style[^>]*>(.*?)</style>`)
	reScriptBlock = regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)
	reSprite      = regexp.MustCompile(`(?s)<svg[^>]*class="sprite".*?</svg>`)
	reSymbol      = regexp.MustCompile(`<symbol id="i-([a-z-]+)"`)
	reIconRef     = regexp.MustCompile(`#i-([a-z-]+)`)
	reScriptIcon  = regexp.MustCompile(`icon\('([a-z-]+)'`)
	reQuoted      = regexp.MustCompile(`'([a-z]+(?:-[a-z]+)*)'`)
	reSVG         = regexp.MustCompile(`<svg[^>]*>`)
	reIconButton  = regexp.MustCompile(`<(a|button) class="ib[^"]*"[^>]*>`)
	reInvisible   = regexp.MustCompile("[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\u009f\u061c\u200b-\u200f\u202a-\u202e\u2060-\u2069\ufeff]")
)

// TestPageAssets: every page inlines only what it uses, within the size
// budget, and the folder page of a share with uploads is the only one with the
// upload client.
func TestPageAssets(t *testing.T) {
	pages := renderedPages(t, "files", []string{"notes.txt"})
	for _, c := range []struct {
		page         string
		css, js      int // at most, in bytes
		list, upload bool
	}{
		{"folder read-only", 13 << 10, 15 << 10, true, false},
		{"folder", 16<<10 + 512, 26 << 10, true, true},
		{"file", 7 << 10, 3<<10 + 512, false, false},
		{"404", 7 << 10, 3<<10 + 512, false, false},
	} {
		body := pages[c.page].Body.String()
		css, js, sprite := reStyleBlock.FindStringSubmatch(body), reScriptBlock.FindStringSubmatch(body), reSprite.FindString(body)
		if css == nil || js == nil || sprite == "" {
			t.Errorf("%s: no inline stylesheet, script or sprite", c.page)
			continue
		}
		if len(css[1]) > c.css || len(js[1]) > c.js || len(sprite) > 6<<10 {
			t.Errorf("%s: css %d (budget %d), js %d (budget %d), sprite %d bytes", c.page, len(css[1]), c.css, len(js[1]), c.js, len(sprite))
		}
		if strings.Contains(css[1], "/*") || strings.Contains(js[1], "\n//") || strings.Contains(js[1], "\n\t") {
			t.Errorf("%s: inline assets aren't compacted", c.page)
		}
		if list, upload := strings.Contains(js[1], "Intl.Collator"), strings.Contains(js[1], "XMLHttpRequest"); list != c.list || upload != c.upload {
			t.Errorf("%s: list script %v, upload script %v", c.page, list, upload)
		}
		if strings.Contains(body, "data-pick") != c.upload || strings.Contains(css[1], ".queue{") != c.upload {
			t.Errorf("%s: upload controls or styles don't match the share", c.page)
		}
	}
}

// TestAssetFiles: the embedded assets hold no raw invisible characters (the
// scripts write them as \u escapes), the scripts build the DOM without HTML
// parsing sinks, and the sprite keeps its license.
func TestAssetFiles(t *testing.T) {
	files, err := fs.Glob(templateFS, "*/*")
	if err != nil || len(files) < 10 {
		t.Fatalf("assets: %v %v", files, err)
	}
	for _, f := range files {
		b, _ := templateFS.ReadFile(f)
		if loc := reInvisible.FindIndex(b); loc != nil {
			t.Errorf("%s: raw invisible character %q at byte %d", f, b[loc[0]:loc[1]], loc[0])
		}
		if !strings.HasSuffix(f, ".js") {
			continue
		}
		for _, sink := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "Function(", "DOMParser", "javascript:"} {
			if strings.Contains(string(b), sink) {
				t.Errorf("%s uses %s", f, sink)
			}
		}
		if strings.Contains(string(b), "`") {
			t.Errorf("%s has a template literal, which compactJS may break across lines", f)
		}
	}
	svg := asset("icons.svg")
	for _, want := range []string{"ISC License", "Copyright (c) 2026 Lucide Icons and Contributors", "Permission to use, copy, modify, and/or distribute this software", "Copyright (c) 2013-present Cole Bemis"} {
		if !strings.Contains(svg, want) {
			t.Errorf("icons.svg lacks %q", want)
		}
	}
	if s := icons(svg); strings.Contains(s, "Permission") || !strings.HasPrefix(s, "<!--") || !strings.HasSuffix(s, "</svg>") {
		t.Errorf("inline sprite = %.80q...", s)
	}
}

// TestIcons: every icon the pages and scripts use is in the sprite, and the
// sprite holds no icon nobody uses.
func TestIcons(t *testing.T) {
	symbols := map[string]bool{}
	for _, m := range reSymbol.FindAllStringSubmatch(asset("icons.svg"), -1) {
		symbols[m[1]] = true
	}
	used := map[string]bool{}
	for name, w := range renderedPages(t, "files", []string{"notes.txt", "a.png", "b.zip"}) {
		for _, m := range reIconRef.FindAllStringSubmatch(w.Body.String(), -1) {
			if used[m[1]] = true; !symbols[m[1]] {
				t.Errorf("%s uses icon %q, which the sprite lacks", name, m[1])
			}
		}
	}
	for _, f := range []string{"base.js", "list.js", "upload.js"} {
		js := asset(f)
		for _, m := range reScriptIcon.FindAllStringSubmatch(js, -1) {
			if !symbols[m[1]] {
				t.Errorf("%s uses icon %q, which the sprite lacks", f, m[1])
			}
		}
		for _, m := range reQuoted.FindAllStringSubmatch(js, -1) {
			used[m[1]] = true
		}
	}
	for kind, name := range kindIcons {
		if used[name] = true; !symbols[name] {
			t.Errorf("kind %s has icon %q, which the sprite lacks", kind, name)
		}
	}
	for name := range symbols {
		if !used[name] {
			t.Errorf("the sprite has %q, which nothing uses", name)
		}
	}
}

// TestScriptTables: the scripts' copies of the kind and icon tables match the
// server's.
func TestScriptTables(t *testing.T) {
	kinds := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\t(\w+): '([a-z0-9 ]+)',$`).FindAllStringSubmatch(asset("upload.js"), -1) {
		for _, ext := range strings.Fields(m[2]) {
			kinds[ext] = m[1]
		}
	}
	if !maps.Equal(kinds, kindExts) {
		t.Errorf("upload.js KINDS differ from kindExts:\n%v\n%v", kinds, kindExts)
	}
	names := map[string]string{}
	block := regexp.MustCompile(`const ICONS = \{([^}]*)\}`).FindStringSubmatch(asset("list.js"))
	if block == nil {
		t.Fatal("list.js has no ICONS")
	}
	for _, m := range regexp.MustCompile(`(\w+): '([a-z-]+)'`).FindAllStringSubmatch(block[1], -1) {
		names[m[1]] = m[2]
	}
	if !maps.Equal(names, kindIcons) {
		t.Errorf("list.js ICONS differ from kindIcons:\n%v\n%v", names, kindIcons)
	}
}

// TestPagesA11y: landmarks, labels and live regions the pages rely on.
func TestPagesA11y(t *testing.T) {
	pages := renderedPages(t, "files", []string{"notes.txt"})
	for name, w := range pages {
		body := w.Body.String()
		for _, svg := range reSVG.FindAllString(body, -1) {
			if !strings.Contains(svg, `aria-hidden="true"`) {
				t.Errorf("%s: %s isn't hidden from screen readers", name, svg)
			}
		}
		for _, b := range reIconButton.FindAllString(body, -1) {
			if !strings.Contains(b, `aria-label="`) {
				t.Errorf("%s: icon-only control %s has no label", name, b)
			}
		}
		if !strings.Contains(body, `id="basic" role="alert" hidden>`) {
			t.Errorf("%s: no error boundary notice", name)
		}
	}
	for _, name := range []string{"folder", "folder read-only", "empty"} {
		body := pages[name].Body.String()
		for _, want := range []string{
			`<a class="skip" href="#files">Skip to files</a>`, `<section class="card" id="files" aria-label="Files"`,
			`<nav class="crumbs" aria-label="Folder path">`, `<th scope="col" aria-sort="ascending">`,
			`<span class="sr-only">Actions</span>`, `aria-live="polite" data-say>`, `<div class="toasts js-only" aria-live="polite">`,
			`aria-label="Filter files and folders in this folder"`, `<select class="sort-m" aria-label="Sort"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %s", name, want)
			}
		}
	}
	if body := pages["folder"].Body.String(); !strings.Contains(body, `<span class="nm" dir="ltr"><span class="base" dir="auto">notes</span><span class="ext" dir="ltr">.txt</span></span>`) {
		t.Errorf("folder rows don't isolate the stems of names:\n%s", body)
	}
}

var reAutoText = regexp.MustCompile(`dir="auto"[^>]*>([^<]*)<`)

// TestNameBoxes: rows show a name in an LTR box that isolates only its stem,
// so a name that starts with a right-to-left character still shows its real
// extension last: "\u05f3invoice.pdf\u05f3.exe" must not read as
// "exe.\u05f3invoice.pdf\u05f3". TestPageScripts checks the boxes the
// scripts build.
func TestNameBoxes(t *testing.T) {
	stem := "\u05f3invoice.pdf\u05f3"
	dir := makeShare(t, map[string]string{stem + ".exe": "x", "\u05d3\u05d5\u05d7.pdf": "x", "notes.txt": "n"})
	s := newServer(t, Options{Path: dir, Upload: true})
	body := get(s, "/", "text/html").Body.String()
	// The single-file page shows its name in the same box.
	solo := get(newServer(t, Options{Path: filepath.Join(dir, stem+".exe")}), "/", "text/html").Body.String()
	if want := `<p class="title"><span class="nm" dir="ltr"><span class="base" dir="auto">` + stem + `</span><span class="ext" dir="ltr">.exe</span></span></p>`; !strings.Contains(solo, want) {
		t.Errorf("single-file page lacks %s", want)
	}
	for _, want := range []string{
		`<span class="nm" dir="ltr"><span class="base" dir="auto">` + stem + `</span><span class="ext" dir="ltr">.exe</span></span>`,
		`<span class="nm" dir="ltr"><span class="base" dir="auto">` + "\u05d3\u05d5\u05d7" + `</span><span class="ext" dir="ltr">.pdf</span></span>`,
		// the row template, for rows built from the JSON listing
		`<span class="nm" dir="ltr"><span class="base" dir="auto"></span><span class="ext" dir="ltr"></span></span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	for _, m := range reAutoText.FindAllStringSubmatch(body, -1) {
		if strings.HasSuffix(m[1], ".exe") || strings.HasSuffix(m[1], ".pdf") || strings.HasSuffix(m[1], ".txt") {
			t.Errorf("a dir=auto element holds a whole name: %q", m[1])
		}
	}
}

// TestPageIdentity: the chrome follows how this request signed in.
func TestPageIdentity(t *testing.T) {
	s := newServer(t, Options{Path: makeShare(t, map[string]string{"a.txt": "a"}), Upload: true})
	for _, c := range []struct {
		name       string
		header     map[string]string
		want, lack []string
	}{
		{"password", map[string]string{},
			[]string{"#i-lock", ">Signed in with password</span>", "Use curl", "curl asks for the password."}, []string{`class="avatar"`, "Signed in as"}},
		{"oidc", map[string]string{"X-Tund-Auth": "oidc", "X-Tund-User-Email": "alice@example.com"},
			[]string{`<span class="avatar" aria-hidden="true">A</span><span class="user" dir="auto" title="Signed in as alice@example.com">alice@example.com</span>`},
			[]string{"Use curl", "curl -u", "#i-lock"}},
		{"oidc name", map[string]string{"X-Tund-Auth": "oidc", "X-Tund-User-Name": "Ödön Kovács", "X-Tund-User-Email": "o@example.com"},
			[]string{`<span class="avatar" aria-hidden="true">Ö</span>`}, []string{"Use curl"}},
		{"oidc anonymous", map[string]string{"X-Tund-Auth": "oidc"},
			[]string{`<span class="user" dir="auto">Signed in</span>`}, []string{`class="avatar"`, "Signed in as", "Use curl"}},
		{"refused", map[string]string{"X-Tund-Auth": ""},
			[]string{"Not allowed"}, []string{"Signed in", "Sign out", "Use curl"}},
	} {
		r := request("GET", "/", nil)
		r.Header.Set("Accept", "text/html")
		for k, v := range c.header {
			if v == "" {
				r.Header.Del(k)
			} else {
				r.Header.Set(k, v)
			}
		}
		body := do(s, r).Body.String()
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: page lacks %s", c.name, w)
			}
		}
		for _, l := range c.lack {
			if strings.Contains(body, l) {
				t.Errorf("%s: page has %s", c.name, l)
			}
		}
	}
}

func TestErrorPageSubject(t *testing.T) {
	s := newServer(t, Options{Path: makeShare(t, map[string]string{"a/b.txt": "b"})})
	body := get(s, "/a/no%20pe", "text/html").Body.String()
	if want := `<p class="msg">There is no file or folder at <code dir="auto">/a/no pe</code>. It may have been renamed, moved or deleted.</p>`; !strings.Contains(body, want) {
		t.Errorf("404 page lacks %s:\n%s", want, body)
	}
	body = get(s, "/a%2Fb", "text/html").Body.String()
	if want := `<p class="msg">There is no file or folder at this address.</p>`; !strings.Contains(body, want) {
		t.Errorf("404 page lacks %s:\n%s", want, body)
	}
}

func TestCompact(t *testing.T) {
	if got, want := compactJS("const a = 1;\n\n\t// note\n\tif (a) b(); // why\n\t\tc();\n"), "const a = 1;\nif (a) b(); // why\nc();"; got != want {
		t.Errorf("compactJS = %q, want %q", got, want)
	}
	if got, want := compactCSS("/* head */\na{b:c}\n/* one */d{e:url(\"x\")}\n@media (x){\nf{g:h}\n}\n"), `a{b:c}d{e:url("x")}@media (x){f{g:h}}`; got != want {
		t.Errorf("compactCSS = %q, want %q", got, want)
	}
}
