package fileserve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSanitize(t *testing.T) {
	cases := []struct {
		in, want string
		status   int
	}{
		{`..\..\evil.txt`, "evil.txt", 0},
		{`C:\Users\x\a.txt`, "a.txt", 0},
		{"a/b/c.txt", "c.txt", 0},
		{"report.pdf", "report.pdf", 0},
		{"", "", 400},
		{".", "", 400},
		{"..", "", 400},
		{"...", "", 400},
		{" \t ", "", 400},
		{"a/", "", 400},
		{".env", "", 403},
		{".bashrc", "", 403},
		{" .env", "", 403},
		{"prod.env.", "", 403},
		{"prod.env ", "", 403},
		{"id_rſa", "", 403},
		{"secretſ.yml", "", 403},
		{"id_rsa", "", 403},
		{"authorized_keys", "", 403},
		{"credentials.json.bak", "", 403},
		{"CON", "_CON", 0},
		{"nul.tar.gz", "_nul.tar.gz", 0},
		{"COM¹.txt", "_COM¹.txt", 0},
		{"LPT0", "_LPT0", 0},
		{"con .txt", "_con .txt", 0},
		{"conin$", "_conin$", 0},
		{"CONOUT$.log", "_CONOUT$.log", 0},
		{"COM10", "COM10", 0},
		{"console.txt", "console.txt", 0},
		{`a<b>c:d"e|f?g*h.txt`, "a_b_c_d_e_f_g_h.txt", 0},
		{"x\u202egnp.exe", "xgnp.exe", 0},
		{"tab\tname.txt", "tabname.txt", 0},
		{"esc\x1b[31mred.txt", "esc[31mred.txt", 0},
		{"cr\rlf\n.txt", "crlf.txt", 0},
		{"a\x00b.txt", "ab.txt", 0},
		{"c1\u0085\u009b.txt", "c1.txt", 0},
		{"zero\u200bwidth\ufeff.txt", "zerowidth.txt", 0},
		{"word\u2060joiner.txt", "wordjoiner.txt", 0},
		{"keep\u200djoiner\u200c.txt", "keep\u200djoiner\u200c.txt", 0},
		{"e\u200b\u0301.txt", "\u00e9.txt", 0},
		{"-rf", "_-rf", 0},
		{"--checkpoint=1", "_--checkpoint=1", 0},
		{"  spaced  .txt  ", "spaced  .txt", 0},
		{"\u3000ideographic\u3000", "ideographic", 0},
		{"trailing....", "trailing", 0},
		{"\xff\xfe.bin", "_.bin", 0},
		{"cafe\u0301.txt", "caf\u00e9.txt", 0},
		{strings.Repeat("a", 300) + ".txt", strings.Repeat("a", 251) + ".txt", 0},
		{strings.Repeat("漢", 100) + ".txt", strings.Repeat("漢", 83) + ".txt", 0},
		{strings.Repeat("a", 300), strings.Repeat("a", 255), 0},
		{strings.Repeat("a", 254) + " " + "b", strings.Repeat("a", 254), 0},
		{strings.Repeat("a", maxRawName), strings.Repeat("a", 255), 0},
		{strings.Repeat("a", maxRawName+1), "", 400},
		{strings.Repeat("/", maxRawName) + "a.txt", "", 400}, // measured before anything is cut
		{"desktop.ini", "", 403},
		{"x.lnk", "", 403},
		{"y.url", "", 403},
		{"z.scf", "", 403},
		{"q.library-ms", "", 403},
		{"x.searchConnector-ms", "", 403},
		{"x.desktop", "", 403},
		{"GNUmakefile", "", 403},
		{"conftest.py", "", 403},
		{"Directory.Build.props", "", 403},
		{"compose.override.yaml", "", 403},
		{"eslint.config.js", "", 403},
		{"evil.pth", "", 403},
		{"Thumbs.db", "", 403},
	}
	for _, c := range cases {
		got, p := sanitize(c.in)
		status := 0
		if p != nil {
			status = p.status
		}
		if got != c.want || status != c.status {
			t.Errorf("sanitize(%q) = %q, %d; want %q, %d", c.in, got, status, c.want, c.status)
		}
		if p == nil && len(got) > 255 {
			t.Errorf("sanitize(%q) is %d bytes", c.in, len(got))
		}
	}
	if _, p := sanitize(strings.Repeat("a", 1<<20)); p == nil || p.message != "That file name is too long." || !errors.Is(p.err, ErrBadName) {
		t.Errorf("sanitize(1 MiB name) = %+v", p)
	}
}

func TestClip(t *testing.T) {
	a, d, n := strings.Repeat("a", 255), strings.Repeat("d", 300), strings.Repeat("n", 300)
	for _, c := range []struct{ in, clip, path string }{
		{"report.pdf", "report.pdf", "report.pdf"},
		{a, a, a},
		{a + "b", a + "…", a + "…"},
		{a[1:] + "é", a[1:] + "…", a[1:] + "…"}, // never inside a character
		{"/in/" + n, "/in/" + n[:251] + "…", "/in/" + n[:255] + "…"},
		{"/" + d + "/" + n, "/" + d[:254] + "…", "/" + d[:254] + "…/" + n[:255] + "…"},
		{"/" + d[:200] + "/" + n[:200], "/" + d[:200] + "/" + n[:53] + "…", "/" + d[:200] + "/" + n[:200]},
	} {
		if got := clip(c.in); got != c.clip {
			t.Errorf("clip(%q) = %q, want %q", c.in, got, c.clip)
		}
		if got := clipPath(c.in); got != c.path {
			t.Errorf("clipPath(%q) = %q, want %q", c.in, got, c.path)
		}
	}
}

func TestSplitExtAndFit(t *testing.T) {
	for in, want := range map[string][2]string{
		"archive.tar.gz":      {"archive", ".tar.gz"},
		"x.TAR.ZST":           {"x", ".TAR.ZST"},
		"x.tar.Z":             {"x", ".tar.Z"},
		"README":              {"README", ""},
		"photo.JPG":           {"photo", ".JPG"},
		"v1.2 final":          {"v1.2 final", ""},
		"a.abcdefghijklmnop":  {"a", ".abcdefghijklmnop"},
		"a.abcdefghijklmnopq": {"a.abcdefghijklmnopq", ""},
		"a.tar":               {"a", ".tar"},
		"my file.final draft": {"my file.final draft", ""},
		".tar.gz":             {".tar", ".gz"},
		"tar.gz":              {"tar", ".gz"},
		"_.bashrc":            {"_", ".bashrc"},
	} {
		if stem, ext := splitExt(in); stem != want[0] || ext != want[1] {
			t.Errorf("splitExt(%q) = %q, %q; want %q, %q", in, stem, ext, want[0], want[1])
		}
	}
	// Candidate names: "stem (n).ext", cut to 255 bytes on a rune boundary.
	for _, c := range []struct{ name, want string }{
		{"archive.tar.gz", "archive (1).tar.gz"},
		{"README", "README (1)"},
		{"photo.JPG", "photo (1).JPG"},
		{"v1.2 final", "v1.2 final (1)"},
		{strings.Repeat("a", 251) + ".txt", strings.Repeat("a", 247) + " (1).txt"},
		{strings.Repeat("é", 125) + ".txt", strings.Repeat("é", 123) + " (1).txt"},
	} {
		stem, ext := splitExt(c.name)
		if got := fit(stem, " (1)", ext); got != c.want || len(got) > 255 {
			t.Errorf("candidate of %q = %q (%d bytes), want %q", c.name, got, len(got), c.want)
		}
	}
}

// part is one multipart part; an empty filename makes a plain field.
type part struct{ field, filename, content string }

func multipartParts(t *testing.T, boundary string, parts ...part) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.SetBoundary(boundary); err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		disp := fmt.Sprintf(`form-data; name=%q`, p.field)
		if p.filename != "" {
			disp += fmt.Sprintf(`; filename="%s"`, strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p.filename))
		}
		h.Set("Content-Disposition", disp)
		w, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, p.content)
	}
	mw.Close()
	return buf.String()
}

// multipartBody sends files (name → content) as "file" fields, in name order.
func multipartBody(t *testing.T, boundary string, files map[string]string) string {
	t.Helper()
	var parts []part
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for n := range files {
			if !yield(n) {
				return
			}
		}
	}) {
		parts = append(parts, part{"file", name, files[name]})
	}
	return multipartParts(t, boundary, parts...)
}

func postRequest(target, body, accept string) *http.Request {
	r := request("POST", target, strings.NewReader(body))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=b0und")
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	return r
}

// recorder collects OnUpload calls.
type recorder struct {
	mu  sync.Mutex
	got []Upload
}

func (rec *recorder) add(u Upload) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.got = append(rec.got, u)
}

func (rec *recorder) all() []Upload {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return slices.Clone(rec.got)
}

// tempFiles lists upload temp files anywhere in the share.
func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), tempPrefix) {
			found = append(found, p)
		}
		return nil
	})
	return found
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPut(t *testing.T) {
	dir := makeShare(t, map[string]string{"inbox/report.pdf": "original", "inbox/sub/": "", ".git/": "", "secrets/": "", ".hidden/": ""})
	var rec recorder
	s := newServer(t, Options{Path: dir, Upload: true, OnUpload: rec.add})

	r := request("PUT", "/inbox/new%20one.txt", strings.NewReader("hello"))
	r.Header.Set("Accept", "application/json")
	w := do(s, r)
	if w.Code != http.StatusCreated || w.Header().Get("Location") != "/inbox/new%20one.txt" {
		t.Fatalf("PUT = %d %q %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	want := map[string]any{"name": "new one.txt", "requested": "new one.txt", "size": 5.0, "renamed": false, "url": "/inbox/new%20one.txt"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("PUT JSON = %v, want %v", got, want)
	}
	if readFile(t, filepath.Join(dir, "inbox", "new one.txt")) != "hello" {
		t.Error("content differs")
	}

	// A taken name gets a number; the text answer says so.
	w = do(s, request("PUT", "/inbox/report.pdf", strings.NewReader("visitor")))
	if w.Code != http.StatusCreated || w.Header().Get("Location") != "/inbox/report%20%281%29.pdf" ||
		w.Body.String() != "saved /inbox/report (1).pdf (7 B; report.pdf already existed)\n" {
		t.Fatalf("PUT taken = %d %q %q", w.Code, w.Header().Get("Location"), w.Body)
	}
	if readFile(t, filepath.Join(dir, "inbox", "report.pdf")) != "original" {
		t.Error("an upload replaced report.pdf")
	}
	if fi, err := os.Stat(filepath.Join(dir, "inbox", "report (1).pdf")); err != nil {
		t.Error(err)
	} else if fi.Mode().Perm()&0o111 != 0 {
		t.Errorf("an upload is executable: %v", fi.Mode())
	}

	// Sanitized names: what the visitor sent is reported as requested, and
	// the sanitized name as the one that was taken.
	w = do(s, request("PUT", "/inbox/a%3Cb%3E.txt", strings.NewReader("x")))
	if w.Code != http.StatusCreated || w.Header().Get("Location") != "/inbox/a_b_.txt" {
		t.Errorf("PUT a<b>.txt = %d %q", w.Code, w.Header().Get("Location"))
	}
	w = do(s, request("PUT", "/inbox/a%3Eb%3C.txt", strings.NewReader("y")))
	if w.Code != http.StatusCreated || w.Body.String() != "saved /inbox/a_b_ (1).txt (1 B; a_b_.txt already existed)\n" {
		t.Errorf("PUT a>b<.txt = %d %q", w.Code, w.Body)
	}

	for _, c := range []struct {
		target string
		code   int
		msg    string
	}{
		{"/inbox/", 400, "Add a file name: curl -T file https://share.example/inbox/\n"},
		{"/", 400, "Add a file name: curl -T file https://share.example/\n"},
		// A folder's URL without the slash: never "inbox (1)" beside it.
		{"/inbox", 409, "inbox is a folder: end the URL with a slash to upload into it: curl -T file https://share.example/inbox/\n"},
		{"/inbox/sub", 409, "sub is a folder: end the URL with a slash to upload into it: curl -T file https://share.example/inbox/sub/\n"},
		// Folders visitors can't see answer like any name.
		{"/.git", 403, "Files whose names start with a dot can't be uploaded to this share.\n"},
		{"/secrets", 403, "Files named like this can't be uploaded to this share.\n"},
		{"/inbox/.env", 403, "Files whose names start with a dot can't be uploaded to this share.\n"},
		{"/inbox/id_rsa", 403, "Files named like this can't be uploaded to this share.\n"},
		{"/inbox/desktop.ini", 403, "Files named like this can't be uploaded to this share.\n"},
		{"/inbox/...", 400, "Names can't be empty, start with a dot or be longer than 255 bytes.\n"},
		{"/.git/x.txt", 404, ""},
		{"/secrets/x.txt", 404, ""},
		{"/.hidden/x.txt", 404, ""},
		{"/nope/x.txt", 404, ""},
		{"/inbox/report.pdf/x.txt", 404, ""},
		{"/inbox/sub/x/y.txt", 404, ""},
	} {
		w := do(s, request("PUT", c.target, failRead{t}))
		if w.Code != c.code || c.msg != "" && w.Body.String() != c.msg {
			t.Errorf("PUT %s = %d %q, want %d %q", c.target, w.Code, w.Body, c.code, c.msg)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "inbox (1)")); err == nil {
		t.Error("PUT /inbox saved a file beside the folder")
	}
	// The page's uploader names every file: a file called like a folder
	// next to it is a name conflict like any other.
	r = request("PUT", "/inbox", strings.NewReader("a file named inbox"))
	r.Header.Set("Accept", "application/json")
	if w := do(s, r); w.Code != http.StatusCreated || w.Header().Get("Location") != "/inbox%20%281%29" {
		t.Errorf("JSON PUT /inbox = %d %q %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	if readFile(t, filepath.Join(dir, "inbox (1)")) != "a file named inbox" {
		t.Error("JSON PUT /inbox wasn't saved as inbox (1)")
	}

	// Resuming (curl -C 10) would store the tail as if it were the file;
	// curl -C - sends the whole file with its full range, which is fine.
	r = request("PUT", "/inbox/big.iso", failRead{t})
	r.Header.Set("Content-Range", "bytes 10-19/20")
	r.ContentLength = 10
	if w := do(s, r); w.Code != http.StatusBadRequest || w.Body.String() != "This share can't resume uploads: send the whole file again (without -C).\n" {
		t.Errorf("PUT with a partial range = %d %q", w.Code, w.Body)
	}
	r = request("PUT", "/inbox/whole.bin", strings.NewReader("0123456789"))
	r.Header.Set("Content-Range", "bytes 0-9/10")
	if w := do(s, r); w.Code != http.StatusCreated || readFile(t, filepath.Join(dir, "inbox", "whole.bin")) != "0123456789" {
		t.Errorf("PUT with the whole range = %d %q", w.Code, w.Body)
	}
	if left := tempFiles(t, dir); left != nil {
		t.Errorf("temp files left: %v", left)
	}

	ups := rec.all()
	if len(ups) < 4 {
		t.Fatalf("OnUpload calls: %+v", ups)
	}
	if u := ups[0]; u.Renamed || u.Taken != "" {
		t.Errorf("OnUpload = %+v", u)
	}
	if u := ups[1]; u.Path != "/inbox/report (1).pdf" || u.Requested != "report.pdf" || u.Size != 7 || !u.Renamed ||
		u.Taken != "report.pdf" || u.User != "" || u.Remote != "203.0.113.7" || u.Err != nil {
		t.Errorf("OnUpload = %+v", u)
	}
	if u := ups[3]; u.Path != "/inbox/a_b_ (1).txt" || u.Requested != "a>b<.txt" || !u.Renamed || u.Taken != "a_b_.txt" {
		t.Errorf("OnUpload for a sanitized name = %+v", u)
	}
	reported := map[string]error{}
	for _, u := range ups[4:] {
		reported[u.Path+" "+u.Requested] = u.Err
	}
	for key, want := range map[string]error{
		"/inbox/ ":                      ErrBadName,
		"/inbox inbox":                  ErrIsFolder,
		"/inbox/sub sub":                ErrIsFolder,
		"/inbox/.env .env":              ErrRefused,
		"/inbox/id_rsa id_rsa":          ErrRefused,
		"/inbox/... ...":                ErrBadName,
		"/.git/x.txt x.txt":             ErrNoFolder,
		"/nope/x.txt x.txt":             ErrNoFolder,
		"/inbox/report.pdf/x.txt x.txt": ErrNoFolder,
		"/inbox/big.iso big.iso":        ErrResumed,
	} {
		if got, ok := reported[key]; !ok || !errors.Is(got, want) {
			t.Errorf("OnUpload for %q = %v, want %v (all: %v)", key, got, want, reported)
		}
	}
}

func TestPutIdentity(t *testing.T) {
	var rec recorder
	s := newServer(t, Options{Path: makeShare(t, nil), Upload: true, OnUpload: rec.add})
	r := request("PUT", "/x.txt", strings.NewReader("x"))
	r.Header.Set("X-Tund-Auth", "oidc")
	r.Header.Set("X-Tund-User-Email", "alice@example.com")
	r.Header.Set("X-Tund-User-Name", "Alice")
	do(s, r)
	r = request("PUT", "/y.txt", strings.NewReader("y"))
	r.Header.Set("X-Tund-Auth", "oidc")
	r.Header.Set("X-Tund-User-Username", "bob\x1b[2J")
	do(s, r)
	ups := rec.all()
	if len(ups) != 2 || ups[0].User != "alice@example.com" || ups[1].User != `bob\u001B[2J` {
		t.Errorf("users = %+v", ups)
	}
}

func TestUploadKeepsCaseVariants(t *testing.T) {
	dir := makeShare(t, map[string]string{"report.pdf": "original", "caf\u00e9.txt": "nfc"})
	s := newServer(t, Options{Path: dir, Upload: true})
	insensitive := caseInsensitive(t, dir)

	w := do(s, request("PUT", "/Report.PDF", strings.NewReader("visitor")))
	want := "/Report.PDF"
	if insensitive {
		want = "/Report%20%281%29.PDF"
	}
	if w.Code != http.StatusCreated || w.Header().Get("Location") != want {
		t.Errorf("PUT Report.PDF = %d %q, want %q", w.Code, w.Header().Get("Location"), want)
	}
	if readFile(t, filepath.Join(dir, "report.pdf")) != "original" {
		t.Error("report.pdf was replaced")
	}
	// NFD names are saved in NFC, so they collide everywhere.
	w = do(s, request("PUT", "/cafe%CC%81.txt", strings.NewReader("nfd")))
	if w.Code != http.StatusCreated || w.Header().Get("Location") != "/caf%C3%A9%20%281%29.txt" {
		t.Errorf("PUT NFD café = %d %q", w.Code, w.Header().Get("Location"))
	}
	if readFile(t, filepath.Join(dir, "caf\u00e9.txt")) != "nfc" {
		t.Error("café.txt was replaced")
	}
}

// caseInsensitive probes whether dir's file system folds case.
func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	p := filepath.Join(dir, ".Probe")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	_, err := os.Stat(filepath.Join(dir, ".pROBE"))
	return err == nil
}

func TestConcurrentSameName(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint("fallback=", fallback), func(t *testing.T) {
			if fallback {
				old := linkFile
				linkFile = func(*os.Root, string, string) error { return errors.New("hard links not supported") }
				t.Cleanup(func() { linkFile = old })
			}
			dir := makeShare(t, map[string]string{"a.txt": "owner", "a (5).txt": "owner5"})
			s := newServer(t, Options{Path: dir, Upload: true})
			const n = 32
			var wg sync.WaitGroup
			codes := make([]int, n)
			for i := range n {
				wg.Go(func() {
					w := do(s, request("PUT", "/a.txt", strings.NewReader(fmt.Sprintf("upload %02d", i))))
					codes[i] = w.Code
				})
			}
			wg.Wait()
			for i, c := range codes {
				if c != http.StatusCreated {
					t.Errorf("upload %d = %d", i, c)
				}
			}
			if s.noLink.Load() != fallback {
				t.Errorf("noLink = %v", s.noLink.Load())
			}
			entries, _ := os.ReadDir(dir)
			var contents []string
			for _, e := range entries {
				c := readFile(t, filepath.Join(dir, e.Name()))
				switch e.Name() {
				case "a.txt":
					if c != "owner" {
						t.Errorf("a.txt = %q", c)
					}
				case "a (5).txt":
					if c != "owner5" {
						t.Errorf("a (5).txt = %q", c)
					}
				default:
					contents = append(contents, c)
				}
			}
			sort.Strings(contents)
			for i := range n {
				if i >= len(contents) || contents[i] != fmt.Sprintf("upload %02d", i) {
					t.Fatalf("uploads stored as %q", contents)
				}
			}
			if len(contents) != n {
				t.Errorf("%d files for %d uploads", len(contents), n)
			}
		})
	}
}

func TestFallbackNeverOverwrites(t *testing.T) {
	old := linkFile
	linkFile = func(*os.Root, string, string) error { return errors.New("hard links not supported") }
	t.Cleanup(func() { linkFile = old })
	dir := makeShare(t, map[string]string{"report.pdf": "original", "sub/": ""})
	s := newServer(t, Options{Path: dir, Upload: true})
	// "sub." is "sub" once sanitized: the folder's name, reserved like any other.
	for _, name := range []string{"report.pdf", "Report.PDF", "sub."} {
		if w := do(s, request("PUT", "/"+name, strings.NewReader("visitor"))); w.Code != http.StatusCreated {
			t.Errorf("PUT %s = %d", name, w.Code)
		}
	}
	if readFile(t, filepath.Join(dir, "report.pdf")) != "original" {
		t.Error("report.pdf was replaced")
	}
	if fi, err := os.Stat(filepath.Join(dir, "sub")); err != nil || !fi.IsDir() {
		t.Error("the folder sub was replaced")
	}
	if left := tempFiles(t, dir); left != nil {
		t.Errorf("temp files left: %v", left)
	}
	if len(s.hidden) != 0 || len(s.temps) != 0 {
		t.Errorf("bookkeeping left: hidden %v temps %v", s.hidden, s.temps)
	}
}

func TestAllNamesTaken(t *testing.T) {
	files := map[string]string{"n.txt": "0"}
	for i := 1; i < 1000; i++ {
		files[fmt.Sprintf("n (%d).txt", i)] = "x"
	}
	dir := makeShare(t, files)
	s := newServer(t, Options{Path: dir, Upload: true})
	if w := do(s, request("PUT", "/n.txt", strings.NewReader("new"))); w.Code != http.StatusConflict {
		t.Errorf("PUT with 1000 names taken = %d %s", w.Code, w.Body)
	}
	if left := tempFiles(t, dir); left != nil {
		t.Errorf("temp files left: %v", left)
	}
}

func TestPost(t *testing.T) {
	dir := makeShare(t, map[string]string{"inbox/b.txt": "owner"})
	var rec recorder
	s := newServer(t, Options{Path: dir, Upload: true, OnUpload: rec.add})

	// A browser's form: 303 back to the folder with a summary.
	body := multipartParts(t, "b0und", part{"file", "a.txt", "A"}, part{"note", "", "ignored"}, part{"file", "b.txt", "B"})
	w := do(s, postRequest("/inbox/", body, "text/html,application/xhtml+xml"))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/inbox/?uploaded=2&renamed=1" {
		t.Fatalf("form POST = %d %q %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	page := get(s, "/inbox/?uploaded=2&renamed=1", "text/html")
	if !strings.Contains(page.Body.String(), "2 files uploaded. 1 was renamed because a file with the same name exists.") {
		t.Errorf("flash missing: %s", page.Body)
	}

	// JSON and text answers; any field name; backslash paths are cut.
	body = multipartParts(t, "b0und", part{"upload[]", `..\..\evil.txt`, "E"}, part{"x", "a.txt", "A2"})
	w = do(s, postRequest("/inbox/", body, "application/json"))
	var got struct {
		Files []savedFile `json:"files"`
	}
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Files) != 2 ||
		got.Files[0].Name != "evil.txt" || got.Files[1].Name != "a (1).txt" || !got.Files[1].Renamed ||
		got.Files[1].URL != "/inbox/a%20%281%29.txt" {
		t.Errorf("JSON POST = %d %s", w.Code, w.Body)
	}
	body = multipartParts(t, "b0und", part{"file", "c.txt", "C"})
	w = do(s, postRequest("/inbox/", body, ""))
	if w.Code != http.StatusCreated || w.Body.String() != "saved /inbox/c.txt (1 B)\n" || w.Header().Get("Location") != "/inbox/c.txt" {
		t.Errorf("text POST = %d %q", w.Code, w.Body)
	}
	for name, want := range map[string]string{"a.txt": "A", "b.txt": "owner", "b (1).txt": "B", "evil.txt": "E", "a (1).txt": "A2", "c.txt": "C"} {
		if got := readFile(t, filepath.Join(dir, "inbox", name)); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	// A refused part stops the upload; what was saved stays and is listed.
	body = multipartParts(t, "b0und", part{"file", "ok.txt", "ok"}, part{"file", ".env", "SECRET=1"}, part{"file", "later.txt", "l"})
	w = do(s, postRequest("/inbox/", body, "application/json"))
	var failed struct {
		Error string
		Files []savedFile
	}
	json.Unmarshal(w.Body.Bytes(), &failed)
	if w.Code != http.StatusForbidden || len(failed.Files) != 1 || failed.Files[0].Name != "ok.txt" || failed.Error == "" {
		t.Errorf("partial POST = %d %s", w.Code, w.Body)
	}
	w = do(s, postRequest("/inbox/", body, ""))
	if !strings.HasPrefix(w.Body.String(), "saved /inbox/ok (1).txt (2 B; ok.txt already existed)\n") {
		t.Errorf("partial POST text = %q", w.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "inbox", "later.txt")); err == nil {
		t.Error("parts after a refused one were saved")
	}

	// Each failure is reported as what it is.
	const mp = "multipart/form-data; boundary=b0und"
	for _, c := range []struct {
		name, ctype, body string
		err               error
		msg               string
	}{
		{"no files", mp, multipartParts(t, "b0und", part{"note", "", "x"}), ErrNotUpload, "The upload had no files.\n"},
		{"empty file name", mp, "--b0und\r\nContent-Disposition: form-data; name=\"file\"; filename=\"\"\r\n\r\n\r\n--b0und--\r\n", ErrNotUpload, "The upload had no files.\n"},
		{"not multipart", "application/octet-stream", "data", ErrNotUpload, "Send files as multipart/form-data, or PUT them one at a time.\n"},
		{"form fields", "application/x-www-form-urlencoded", "x=1", ErrNotUpload, "Send files as multipart/form-data, or PUT them one at a time.\n"},
		{"other boundary", mp, multipartParts(t, "other", part{"file", "a.txt", "A"}), ErrNotUpload, "This upload isn't well-formed multipart/form-data.\n"},
		{"empty body", mp, "", ErrNotUpload, "This upload isn't well-formed multipart/form-data.\n"},
		{"cut part", mp, "--b0und\r\nContent-Disposition: form-data; name=\"file\"; filename=\"cut.txt\"\r\n\r\npartial", ErrCanceled, "The upload broke off before cut.txt was complete.\n"},
	} {
		r := postRequest("/inbox/", c.body, "")
		r.Header.Set("Content-Type", c.ctype)
		w := do(s, r)
		ups := rec.all()
		if w.Code != http.StatusBadRequest || w.Body.String() != c.msg || !errors.Is(ups[len(ups)-1].Err, c.err) {
			t.Errorf("%s: POST = %d %q, OnUpload %v", c.name, w.Code, w.Body, ups[len(ups)-1].Err)
		}
	}
	// A body that breaks off is the visitor leaving, not a malformed upload.
	cut := postRequest("/inbox/", "", "")
	cut.Body = io.NopCloser(io.MultiReader(strings.NewReader("--b0und\r\nContent-Disp"), brokenBody{}))
	if w := do(s, cut); w.Code != http.StatusBadRequest || w.Body.String() != "The upload broke off before it was complete.\n" {
		t.Errorf("POST cut in a header = %d %q", w.Code, w.Body)
	}
	if ups := rec.all(); !errors.Is(ups[len(ups)-1].Err, ErrCanceled) {
		t.Errorf("POST cut in a header: OnUpload %+v", ups[len(ups)-1])
	}
	if _, err := os.Stat(filepath.Join(dir, "inbox", "cut.txt")); err == nil {
		t.Error("a broken part was saved")
	}

	// Forms escape ", CR and LF in file names: undone, then sanitized like
	// any name. Other escapes are the name's own.
	body = multipartParts(t, "b0und", part{"file", "say %22hi%22.txt", "1"}, part{"file", "line%0Abreak%0D.txt", "2"}, part{"file", "50%25 off.txt", "3"})
	if w := do(s, postRequest("/inbox/", body, "")); w.Code != http.StatusCreated {
		t.Errorf("POST with form escapes = %d %q", w.Code, w.Body)
	}
	for name, want := range map[string]string{"say _hi_.txt": "1", "linebreak.txt": "2", "50%25 off.txt": "3"} {
		if got := readFile(t, filepath.Join(dir, "inbox", name)); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if left := tempFiles(t, dir); left != nil {
		t.Errorf("temp files left: %v", left)
	}
	if w := do(s, postRequest("/nope/", body, "")); w.Code != http.StatusNotFound {
		t.Errorf("POST to a missing folder = %d", w.Code)
	}
	if w := do(s, postRequest("/inbox/b.txt", body, "")); w.Code != http.StatusNotFound {
		t.Errorf("POST to a file = %d", w.Code)
	}
}

// brokenBody is a request body whose visitor went away.
type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestPostPartLimit(t *testing.T) {
	old := maxParts
	maxParts = 5
	t.Cleanup(func() { maxParts = old })
	dir := makeShare(t, nil)
	var rec recorder
	s := newServer(t, Options{Path: dir, Upload: true, OnUpload: rec.add})
	var parts []part
	for i := range 6 {
		parts = append(parts, part{"f", "", fmt.Sprint(i)})
	}
	if w := do(s, postRequest("/", multipartParts(t, "b0und", parts...), "")); w.Code != http.StatusBadRequest {
		t.Errorf("6 parts = %d %s", w.Code, w.Body)
	}
	if ups := rec.all(); len(ups) != 1 || !errors.Is(ups[0].Err, ErrTooManyParts) {
		t.Errorf("OnUpload = %+v", ups)
	}
	if w := do(s, postRequest("/", multipartParts(t, "b0und", append(parts[:4], part{"f", "ok.txt", "x"})...), "")); w.Code != http.StatusCreated {
		t.Errorf("5 parts = %d %s", w.Code, w.Body)
	}
}

func TestFreeSpaceFloor(t *testing.T) {
	oldSpace, oldEvery := diskSpace, floorEvery
	t.Cleanup(func() { diskSpace, floorEvery = oldSpace, oldEvery })
	var mu sync.Mutex
	free := uint64(1<<30 + 1000) // 1000 bytes above the floor of a 100 GiB volume
	diskSpace = func(string) (uint64, uint64, error) {
		mu.Lock()
		defer mu.Unlock()
		return free, 100 << 30, nil
	}
	setFree := func(n uint64) { mu.Lock(); free = n; mu.Unlock() }

	dir := makeShare(t, map[string]string{"in/": ""})
	var rec recorder
	s := newServer(t, Options{Path: dir, Upload: true, OnUpload: rec.add})

	// Known lengths beyond the floor are refused before reading.
	r := request("PUT", "/in/big.bin", failRead{t})
	r.ContentLength = 2000
	if w := do(s, r); w.Code != http.StatusInsufficientStorage {
		t.Errorf("PUT over the floor = %d", w.Code)
	}
	r = postRequest("/in/", "", "")
	r.Body, r.ContentLength = io.NopCloser(failRead{t}), 5000
	if w := do(s, r); w.Code != http.StatusInsufficientStorage {
		t.Errorf("POST over the floor = %d", w.Code)
	}
	if w := do(s, request("PUT", "/in/fits.bin", strings.NewReader(strings.Repeat("x", 900)))); w.Code != http.StatusCreated {
		t.Errorf("PUT within the floor = %d", w.Code)
	}

	// Streams are re-checked as they grow.
	floorEvery = 1024
	setFree(100 << 30)
	r = request("PUT", "/in/stream.bin", &slowFill{n: 10 << 10, after: 2048, do: func() { setFree(1 << 20) }})
	r.ContentLength = -1
	w := do(s, r)
	if w.Code != http.StatusInsufficientStorage {
		t.Errorf("PUT crossing the floor = %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "in", "stream.bin")); err == nil {
		t.Error("an aborted upload was saved")
	}
	if left := tempFiles(t, dir); left != nil {
		t.Errorf("temp files left: %v", left)
	}
	ups := rec.all()
	if last := ups[len(ups)-1]; !errors.Is(last.Err, ErrDiskFull) || last.Path != "/in/stream.bin" {
		t.Errorf("OnUpload = %+v", last)
	}

	// Every file of a form is checked before it is saved: small parts never
	// reach the re-check while they stream.
	floorEvery = oldEvery
	setFree(100 << 30)
	filling := newServer(t, Options{Path: dir, Upload: true, OnUpload: func(u Upload) {
		if u.Err == nil {
			setFree(1 << 20) // the first file fills the disk
		}
	}})
	body := multipartParts(t, "b0und", part{"file", "one.txt", "1"}, part{"file", "two.txt", "2"})
	if w := do(filling, postRequest("/in/", body, "")); w.Code != http.StatusInsufficientStorage ||
		w.Body.String() != "saved /in/one.txt (1 B)\nThe owner's disk is full, so two.txt wasn't saved.\n" {
		t.Errorf("POST over the floor after a part = %d %q", w.Code, w.Body)
	}

	// Volumes that report no size at all (FUSE without statfs, ramfs).
	diskSpace = func(string) (uint64, uint64, error) { return 0, 0, nil }
	if w := do(s, request("PUT", "/in/fuse.bin", strings.NewReader("12345"))); w.Code != http.StatusCreated {
		t.Errorf("PUT to a volume without a size = %d %q", w.Code, w.Body)
	}
}

// TestFreeSpaceRecheck: an upload that started above the floor stops within
// 16 MiB of the disk crossing it, so concurrent uploads overshoot little.
func TestFreeSpaceRecheck(t *testing.T) {
	oldSpace := diskSpace
	t.Cleanup(func() { diskSpace = oldSpace })
	var mu sync.Mutex
	free := uint64(100 << 30)
	diskSpace = func(string) (uint64, uint64, error) {
		mu.Lock()
		defer mu.Unlock()
		return free, 100 << 30, nil
	}
	dir := makeShare(t, map[string]string{"in/": ""})
	var rec recorder
	s := newServer(t, Options{Path: dir, Upload: true, OnUpload: rec.add})
	r := request("PUT", "/in/huge.bin", &slowFill{n: 40 << 20, after: 1 << 20, do: func() { mu.Lock(); free = 1 << 20; mu.Unlock() }})
	if w := do(s, r); w.Code != http.StatusInsufficientStorage {
		t.Errorf("PUT crossing the floor = %d %q", w.Code, w.Body)
	}
	if ups := rec.all(); len(ups) != 1 || !errors.Is(ups[0].Err, ErrDiskFull) || ups[0].Size > 16<<20 {
		t.Errorf("OnUpload = %+v", ups)
	}
	if left := tempFiles(t, dir); left != nil {
		t.Errorf("temp files left: %v", left)
	}
}

// slowFill yields n bytes and calls do once after the first after bytes.
type slowFill struct {
	n, after, sent int
	do             func()
}

func (f *slowFill) Read(p []byte) (int, error) {
	if f.sent >= f.n {
		return 0, io.EOF
	}
	k := min(len(p), 512, f.n-f.sent)
	if f.sent < f.after && f.sent+k >= f.after {
		f.do()
	}
	f.sent += k
	return k, nil
}

// rawStatus sends a request head over a new connection and returns the first
// status line the server answers with. The caller closes the connection.
func rawStatus(t *testing.T, addr, head string) (string, net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, head); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(line), conn, br
}

func TestExpectContinueRefusedEarly(t *testing.T) {
	oldSpace := diskSpace
	t.Cleanup(func() { diskSpace = oldSpace })
	diskSpace = func(string) (uint64, uint64, error) { return 2 << 30, 100 << 30, nil }
	dir := makeShare(t, map[string]string{"in/": ""})
	ro := httptest.NewServer(newServer(t, Options{Path: dir}))
	defer ro.Close()
	rw := httptest.NewServer(newServer(t, Options{Path: dir, Upload: true}))
	defer rw.Close()
	head := func(path string, length int64, extra string) string {
		return fmt.Sprintf("PUT %s HTTP/1.1\r\nHost: share.example\r\nX-Tund-Auth: password\r\nContent-Length: %d\r\nExpect: 100-continue\r\n%s\r\n", path, length, extra)
	}
	for _, c := range []struct {
		srv    *httptest.Server
		path   string
		length int64
		extra  string // more header lines
		want   string
	}{
		{ro, "/in/a.txt", 5, "", "HTTP/1.1 405 Method Not Allowed"},
		{rw, "/in/.env", 5, "", "HTTP/1.1 403 Forbidden"},
		{rw, "/nope/a.txt", 5, "", "HTTP/1.1 404 Not Found"},
		{rw, "/in/", 5, "", "HTTP/1.1 400 Bad Request"},
		{rw, "/in", 5, "", "HTTP/1.1 409 Conflict"},
		{rw, "/in/part.bin", 5, "Content-Range: bytes 10-14/15\r\n", "HTTP/1.1 400 Bad Request"},
		{rw, "/in/huge.iso", 2 << 30, "", "HTTP/1.1 507 Insufficient Storage"},
		{rw, "/in/a.txt", 5, "", "HTTP/1.1 100 Continue"},
	} {
		line, conn, br := rawStatus(t, c.srv.Listener.Addr().String(), head(c.path, c.length, c.extra))
		if line != c.want {
			t.Errorf("PUT %s = %q, want %q", c.path, line, c.want)
		} else if c.want == "HTTP/1.1 100 Continue" {
			br.ReadString('\n') // the blank line that ends the interim response
			io.WriteString(conn, "hello")
			resp, err := http.ReadResponse(br, nil)
			if err != nil || resp.StatusCode != http.StatusCreated {
				t.Errorf("after 100 Continue: %v %v", resp, err)
			}
		}
		conn.Close() // a refused body is never sent; the server waits for the rest otherwise
	}
	if readFile(t, filepath.Join(dir, "in", "a.txt")) != "hello" {
		t.Error("the accepted upload is missing")
	}
}

func TestVisitorAbortRemovesTemp(t *testing.T) {
	dir := makeShare(t, map[string]string{"in/": ""})
	var rec recorder
	srv := httptest.NewServer(newServer(t, Options{Path: dir, Upload: true, OnUpload: rec.add}))
	defer srv.Close()

	for i, chunked := range []bool{false, true} {
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if chunked {
			body := multipartParts(t, "b0und", part{"file", "cut.bin", strings.Repeat("x", 64<<10)})
			fmt.Fprintf(conn, "POST /in/ HTTP/1.1\r\nHost: share.example\r\nX-Tund-Auth: password\r\nContent-Type: multipart/form-data; boundary=b0und\r\nTransfer-Encoding: chunked\r\n\r\n")
			half := body[:len(body)/2]
			fmt.Fprintf(conn, "%x\r\n%s\r\n", len(half), half)
		} else {
			fmt.Fprintf(conn, "PUT /in/cut.bin HTTP/1.1\r\nHost: share.example\r\nX-Tund-Auth: password\r\nContent-Length: %d\r\n\r\n", 1<<20)
			conn.Write(bytes.Repeat([]byte("x"), 300<<10))
		}
		waitFor(t, func() bool { return len(tempFiles(t, dir)) > 0 })
		conn.Close()
		waitFor(t, func() bool { return len(tempFiles(t, dir)) == 0 })
		waitFor(t, func() bool { return len(rec.all()) == i+1 })
		ups := rec.all()
		if u := ups[len(ups)-1]; !errors.Is(u.Err, ErrCanceled) || u.Path != "/in/cut.bin" || u.Size == 0 {
			t.Errorf("chunked=%v: OnUpload = %+v", chunked, u)
		}
		if _, err := os.Stat(filepath.Join(dir, "in", "cut.bin")); err == nil {
			t.Errorf("chunked=%v: a canceled upload was saved", chunked)
		}
	}
}

// endHook is a request body that calls do as it hands out its last byte,
// when net/http starts watching for the visitor to leave.
type endHook struct {
	r  *strings.Reader
	do func()
}

func (e *endHook) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if e.r.Len() == 0 && e.do != nil {
		e.do()
		e.do = nil
	}
	return n, err
}

// TestVisitorLeftWhileSaving: a visitor who leaves once the whole file was
// sent, while it is being saved ("Saving…", "Cancel"), doesn't get it saved.
func TestVisitorLeftWhileSaving(t *testing.T) {
	dir := makeShare(t, map[string]string{"in/": ""})
	var rec recorder
	s := newServer(t, Options{Path: dir, Upload: true, OnUpload: rec.add})
	leaving := func(r *http.Request, body string) *http.Request {
		ctx, cancel := context.WithCancel(r.Context())
		t.Cleanup(cancel)
		r.Body = io.NopCloser(&endHook{r: strings.NewReader(body), do: cancel})
		return r.WithContext(ctx)
	}
	do(s, leaving(request("PUT", "/in/late.txt", nil), "all of it"))
	body := multipartParts(t, "b0und", part{"file", "a.txt", "A"}, part{"file", "b.txt", "B"})
	do(s, leaving(postRequest("/in/", body, ""), body))
	for _, name := range []string{"late.txt", "a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dir, "in", name)); err == nil {
			t.Errorf("%s was saved after its visitor left", name)
		}
	}
	if left := tempFiles(t, dir); left != nil {
		t.Errorf("temp files left: %v", left)
	}
	ups := rec.all()
	if len(ups) != 2 || !errors.Is(ups[0].Err, ErrCanceled) || ups[0].Size != 9 || !errors.Is(ups[1].Err, ErrCanceled) {
		t.Errorf("OnUpload = %+v", ups)
	}
}

// TestLongNames: responses and OnUpload repeat at most 255 bytes of a name a
// visitor sent, however long it is.
func TestLongNames(t *testing.T) {
	dir := makeShare(t, map[string]string{"in/": ""})
	var rec recorder
	s := newServer(t, Options{Path: dir, Upload: true, OnUpload: rec.add})
	bounded := func(s string) bool { return len(s) <= maxShown+len("…") }

	// Saved under its first 255 bytes; the answer says what was asked for, cut.
	long := strings.Repeat("a", 3000) + ".txt"
	r := request("PUT", "/in/"+long, strings.NewReader("x"))
	r.Header.Set("Accept", "application/json")
	w := do(s, r)
	var got savedFile
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusCreated ||
		got.Name != strings.Repeat("a", 251)+".txt" || got.Requested != strings.Repeat("a", 255)+"…" {
		t.Errorf("PUT a 3000-byte name = %d %s", w.Code, w.Body)
	}

	// Longer than 4 KiB: refused before anything else, in PUTs and forms.
	w = do(s, request("PUT", "/in/"+strings.Repeat("b", 5000), failRead{t}))
	if w.Code != http.StatusBadRequest || w.Body.String() != "That file name is too long.\n" {
		t.Errorf("PUT a 5000-byte name = %d %q", w.Code, w.Body)
	}
	body := multipartParts(t, "b0und", part{"file", strings.Repeat("<", 1<<20), "x"}, part{"file", strings.Repeat("c", 1<<20), "y"})
	w = do(s, postRequest("/in/", body, "application/json"))
	if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"That file name is too long."}`+"\n" {
		t.Errorf("POST 1 MiB names = %d %.200q", w.Code, w.Body)
	}

	// A 404 repeats at most 255 bytes of the path, however it is escaped.
	w = get(s, "/in/"+strings.Repeat("%3C", 5000), "application/json")
	msg, _ := json.Marshal(map[string]string{"error": "There is no file or folder at /in/" + strings.Repeat("<", 251) + "…. It may have been renamed, moved or deleted."})
	if w.Code != http.StatusNotFound || w.Body.String() != string(msg)+"\n" {
		t.Errorf("GET a 5000-byte name = %d %.200q", w.Code, w.Body)
	}

	ups := rec.all()
	if len(ups) != 3 {
		t.Fatalf("OnUpload = %d calls", len(ups))
	}
	for _, u := range ups {
		if !bounded(u.Requested) || !bounded(u.Taken) || !strings.HasPrefix(u.Path, "/in/") || !bounded(u.Path[len("/in/"):]) {
			t.Errorf("OnUpload repeats %d bytes: %.80q...", len(u.Path)+len(u.Requested), u.Requested)
		}
	}
	if !errors.Is(ups[2].Err, ErrBadName) || ups[2].Requested != strings.Repeat(`<`, 255)+"…" {
		t.Errorf("OnUpload for a 1 MiB name = %.80q... %v", ups[2].Requested, ups[2].Err)
	}
}

func TestCloseCancelsUploads(t *testing.T) {
	dir := makeShare(t, map[string]string{"in/": ""})
	var rec recorder
	s, err := New(Options{Path: dir, Upload: true, OnUpload: rec.add})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "PUT /in/slow.bin HTTP/1.1\r\nHost: share.example\r\nX-Tund-Auth: password\r\nContent-Length: %d\r\n\r\n", 1<<20)
	conn.Write(bytes.Repeat([]byte("x"), 100<<10))
	waitFor(t, func() bool { return len(tempFiles(t, dir)) > 0 })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close = %v after %v", err, time.Since(start))
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Close took %v", d)
	}
	if left := tempFiles(t, dir); left != nil {
		t.Errorf("temp files left after Close: %v", left)
	}
	ups := rec.all()
	if len(ups) != 1 || !errors.Is(ups[0].Err, ErrStopped) {
		t.Errorf("OnUpload = %+v", ups)
	}
	if w := do(s, request("GET", "/", nil)); w.Code != http.StatusServiceUnavailable {
		t.Errorf("GET after Close = %d", w.Code)
	}
}

func TestCloseRemovesLeftovers(t *testing.T) {
	dir := makeShare(t, map[string]string{"in/": ""})
	s, err := New(Options{Path: dir, Upload: true})
	if err != nil {
		t.Fatal(err)
	}
	// An upload whose handler can't finish (it is stuck past Close's deadline).
	f, err := s.fsys.OpenFile("in/"+tempPrefix+"stuck", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	s.track("in/" + tempPrefix + "stuck")
	if !s.beginUpload() {
		t.Fatal("beginUpload refused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Close = %v, want the deadline", err)
	}
	if left := tempFiles(t, dir); left != nil {
		t.Errorf("temp files left: %v", left)
	}
	s.endUpload()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
