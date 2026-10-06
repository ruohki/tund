package fileserve

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSplitPath(t *testing.T) {
	ok := []struct {
		in   string
		segs []string
		dir  bool
	}{
		{"/", nil, true},
		{"/a", []string{"a"}, false},
		{"/a/", []string{"a"}, true},
		{"/a/b.txt", []string{"a", "b.txt"}, false},
		{"/a%20b/c%3Fd", []string{"a b", "c?d"}, false},
		{"/pct%2541/", []string{"pct%41"}, true},
		{"/caf%C3%A9", []string{"café"}, false},
		{"/caf%E9", []string{"caf\xe9"}, false},
		{"/a+b", []string{"a+b"}, false},
		{"/...", []string{"..."}, false},
	}
	for _, c := range ok {
		segs, dir, valid := splitPath(c.in)
		if !valid || !slices.Equal(segs, c.segs) || dir != c.dir {
			t.Errorf("splitPath(%q) = %q, %v, %v; want %q, %v", c.in, segs, dir, valid, c.segs, c.dir)
		}
	}
	for _, in := range []string{"", "a", "//", "//evil.com", "//evil.com/", "/a//b", "/./a", "/a/.", "/../a",
		"/a/..", "/%2e%2e/x", "/%2E", "/a%2Fb", "/a%2fb", "/a%00b", "/a%5Cb", "/a%5c..%5c.env", "/a%zz", "/a%"} {
		if segs, dir, valid := splitPath(in); valid {
			t.Errorf("splitPath(%q) = %q, %v; want invalid", in, segs, dir)
		}
	}
}

func TestBadPathsAnswer404(t *testing.T) {
	s := newServer(t, Options{Path: makeShare(t, map[string]string{"a/b": "x", "evil.com/x": "y", ".env": "SECRET=1"}), Upload: true})
	for _, p := range []string{"//evil.com", "//evil.com/", "/a//b", "/./a", "/a/./b", "/%2e%2e/x", "/a/%2e%2e/.env", "/a%2Fb",
		"/a%00b", "/a%5Cb", "/a%5C..%5C.env", "/a/b/"} {
		for _, m := range []string{"GET", "HEAD", "PUT", "POST"} {
			if m == "PUT" && strings.HasSuffix(p, "/") {
				continue // PUT to a folder URL asks for a file name (400)
			}
			r := request(m, "", nil)
			r.URL = &url.URL{Path: mustUnescape(p), RawPath: p}
			w := do(s, r)
			if w.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404", m, p, w.Code)
			}
			if loc := w.Header().Get("Location"); loc != "" {
				t.Errorf("%s %s redirected to %q", m, p, loc)
			}
		}
	}
}

func mustUnescape(p string) string {
	s, err := url.PathUnescape(p)
	if err != nil {
		panic(err)
	}
	return s
}

func TestFolderRedirect(t *testing.T) {
	names := []string{"plain", "q?x", "h#sh", "pct%41", "a b", "a:b", "über", "semi;colon", "evil.com", "\u65e5\u672c"}
	files := map[string]string{"sub/": ""}
	for _, n := range names {
		files[n+"/inside.txt"] = n
		files["sub/"+n+"/"] = ""
	}
	s := newServer(t, Options{Path: makeShare(t, files), Upload: true})
	for _, n := range names {
		for _, base := range [][]string{nil, {"sub"}} {
			segs := append(slices.Clone(base), n)
			target := href(segs, false)
			for _, m := range []string{"GET", "HEAD"} {
				w := do(s, request(m, target, nil))
				want := target + "/"
				if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != want {
					t.Errorf("%s %s = %d Location %q, want 301 %q", m, target, w.Code, w.Header().Get("Location"), want)
				}
				if strings.HasPrefix(w.Header().Get("Location"), "//") {
					t.Errorf("%s %s redirects off-site", m, target)
				}
			}
			// The redirect target lists that folder.
			loc, _ := url.Parse("http://share.example" + target + "/")
			if loc.EscapedPath() != target+"/" {
				t.Errorf("Location %q does not round-trip: %q", target+"/", loc.EscapedPath())
			}
			r := request("GET", "", nil)
			r.URL = loc
			if w := do(s, r); w.Code != http.StatusOK {
				t.Errorf("GET %s/ = %d", target, w.Code)
			} else if len(base) == 0 && !strings.Contains(w.Body.String(), "inside.txt") {
				t.Errorf("GET %s/ listed %q", target, w.Body)
			}
		}
	}
	// PUT and POST never redirect: PUT /plain names the folder, which is a
	// 409 that says to add the slash (never a file "plain (1)" beside it).
	w := do(s, request("PUT", "/plain", failRead{t}))
	if w.Code != http.StatusConflict || w.Header().Get("Location") != "" {
		t.Errorf("PUT /plain = %d Location %q", w.Code, w.Header().Get("Location"))
	}
	r := request("POST", "/plain", strings.NewReader(multipartBody(t, "b1", map[string]string{"f.txt": "f"})))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=b1")
	if w := do(s, r); w.Code != http.StatusCreated {
		t.Errorf("POST /plain = %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(s.root.Path, "plain", "f.txt")); err != nil {
		t.Errorf("POST /plain did not save into the folder: %v", err)
	}
}

func TestHref(t *testing.T) {
	cases := []struct {
		segs []string
		dir  bool
		want string
	}{
		{nil, true, "/"},
		{nil, false, "/"},
		{[]string{"a b", "c.txt"}, false, "/a%20b/c.txt"},
		{[]string{"a:b"}, false, "/a:b"},
		{[]string{"javascript:alert(1)"}, false, "/javascript:alert%281%29"},
		{[]string{"q?x"}, true, "/q%3Fx/"},
		{[]string{"h#sh"}, false, "/h%23sh"},
		{[]string{"pct%41"}, true, "/pct%2541/"},
		{[]string{"semi;colon"}, false, "/semi%3Bcolon"},
		{[]string{"it's"}, false, "/it%27s"},
		{[]string{"caf\xe9.txt"}, false, "/caf%E9.txt"},
	}
	for _, c := range cases {
		if got := href(c.segs, c.dir); got != c.want {
			t.Errorf("href(%q, %v) = %q, want %q", c.segs, c.dir, got, c.want)
		}
	}
}

func TestShown(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":              "report.pdf",
		"x\u202egnp.exe":          `x\u202Egnp.exe`,
		"a\u2066b\u2069":          `a\u2066b\u2069`,
		"tab\tname":               `tab\u0009name`,
		"esc\x1b[31m":             `esc\u001B[31m`,
		"caf\xe9":                 "caf\uFFFD",
		"zwj\u200dkept":           "zwj\u200dkept",
		"\u65e5\u672c\u8a9e":      "\u65e5\u672c\u8a9e",
		"right\u200fto\u200eleft": `right\u200Fto\u200Eleft`,
		"c1\u0085":                `c1\u0085`,
	} {
		if got := shown(in); got != want {
			t.Errorf("shown(%q) = %q, want %q", in, got, want)
		}
	}
}
