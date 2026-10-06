package fileserve

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain gives the tests a home folder of their own: on Windows the temp
// folder is inside the real one's AppData, which CheckRoot refuses for uploads.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "fileserve-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// makeShare builds a share folder: keys are slash paths, a trailing "/" makes
// a folder, values are file contents.
func makeShare(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "share")
	if err := os.Mkdir(dir, 0o755); err != nil {
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
	return dir
}

func newServer(t *testing.T, opts Options) *Server {
	t.Helper()
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	return s
}

// request builds a request the way the edge forwards it: signed in with the
// password, from one visitor address.
func request(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, "http://share.example"+target, body)
	r.Header.Set("X-Tund-Auth", "password")
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	return r
}

func do(s *Server, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func get(s *Server, target, accept string) *httptest.ResponseRecorder {
	r := request("GET", target, nil)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	return do(s, r)
}

// listNames returns the entry names of a JSON listing.
func listNames(t *testing.T, s *Server, target string) []string {
	t.Helper()
	w := get(s, target, "application/json")
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", target, w.Code, w.Body)
	}
	var l jsonListing
	if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range l.Entries {
		names = append(names, e.Name)
	}
	return names
}

// failRead is a request body that must never be read.
type failRead struct{ t *testing.T }

func (f failRead) Read([]byte) (int, error) {
	f.t.Error("the body was read")
	return 0, io.ErrUnexpectedEOF
}

func TestAdmission(t *testing.T) {
	dir := makeShare(t, map[string]string{"a.txt": "hello", "sub/": ""})
	ro := newServer(t, Options{Path: dir})
	rw := newServer(t, Options{Path: dir, Upload: true})
	cases := []struct {
		name   string
		s      *Server
		method string
		target string
		header map[string]string
		code   int
		allow  string
	}{
		{"no auth", ro, "GET", "/a.txt", map[string]string{"X-Tund-Auth": ""}, 403, ""},
		{"no auth, missing path", ro, "GET", "/nope", map[string]string{"X-Tund-Auth": ""}, 403, ""},
		{"auth none", ro, "GET", "/a.txt", map[string]string{"X-Tund-Auth": "none"}, 403, ""},
		{"auth other case", ro, "GET", "/a.txt", map[string]string{"X-Tund-Auth": "Password"}, 403, ""},
		{"oidc", ro, "GET", "/a.txt", map[string]string{"X-Tund-Auth": "oidc"}, 200, ""},
		{"replay", ro, "GET", "/a.txt", map[string]string{"X-Forwarded-For": "replay"}, 403, ""},
		{"two addresses", ro, "GET", "/a.txt", map[string]string{"X-Forwarded-For": "203.0.113.7, 198.51.100.1"}, 403, ""},
		{"empty address", ro, "GET", "/a.txt", map[string]string{"X-Forwarded-For": " "}, 403, ""},
		{"spaced address", ro, "GET", "/a.txt", map[string]string{"X-Forwarded-For": " 2001:db8::1 "}, 200, ""},
		{"no address", ro, "GET", "/a.txt", map[string]string{"X-Forwarded-For": ""}, 200, ""},
		{"delete", ro, "DELETE", "/a.txt", nil, 405, "GET, HEAD"},
		{"delete with uploads", rw, "DELETE", "/a.txt", nil, 405, "GET, HEAD, PUT, POST"},
		{"options", rw, "OPTIONS", "/", nil, 405, "GET, HEAD, PUT, POST"},
		{"propfind", ro, "PROPFIND", "/", nil, 405, "GET, HEAD"},
		{"put, uploads off", ro, "PUT", "/b.txt", nil, 405, "GET, HEAD"},
		{"post, uploads off", ro, "POST", "/sub/", nil, 405, "GET, HEAD"},
		{"same-site image", ro, "GET", "/a.txt", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, 403, ""},
		{"cross-site fetch", ro, "GET", "/", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"}, 403, ""},
		{"same-site iframe", ro, "GET", "/a.txt", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}, 403, ""},
		{"cross-site navigation", ro, "GET", "/a.txt", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, 200, ""},
		{"same-site navigation, no dest", ro, "HEAD", "/a.txt", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "navigate"}, 200, ""},
		{"same-origin fetch", ro, "GET", "/a.txt", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors"}, 200, ""},
		{"typed URL", ro, "GET", "/a.txt", map[string]string{"Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate"}, 200, ""},
		{"same-site put", rw, "PUT", "/x1.txt", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://evil.share.example"}, 403, ""},
		{"cross-site post", rw, "POST", "/sub/", map[string]string{"Sec-Fetch-Site": "cross-site", "Content-Type": "multipart/form-data; boundary=x"}, 403, ""},
		{"cross-site navigation post", rw, "POST", "/sub/", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, 403, ""},
		{"old browser, other origin", rw, "PUT", "/x2.txt", map[string]string{"Origin": "https://evil.example"}, 403, ""},
		{"old browser, null origin", rw, "PUT", "/x3.txt", map[string]string{"Origin": "null"}, 403, ""},
		{"old browser, same origin", rw, "PUT", "/x4.txt", map[string]string{"Origin": "http://share.example"}, 201, ""},
		{"curl put", rw, "PUT", "/x5.txt", nil, 201, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body io.Reader = failRead{t}
			if c.code == 201 {
				body = strings.NewReader("data")
			}
			if c.method == "GET" || c.method == "HEAD" {
				body = nil
			}
			r := request(c.method, c.target, body)
			for k, v := range c.header {
				if v == "" {
					r.Header.Del(k)
				} else {
					r.Header.Set(k, v)
				}
			}
			w := do(c.s, r)
			if w.Code != c.code {
				t.Fatalf("%s %s = %d, want %d: %s", c.method, c.target, w.Code, c.code, w.Body)
			}
			if got := w.Header().Get("Allow"); got != c.allow {
				t.Errorf("Allow = %q, want %q", got, c.allow)
			}
			checkSafetyHeaders(t, w.Result())
		})
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tund-upload-") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
	for _, n := range []string{"x1.txt", "x2.txt", "x3.txt"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("refused upload %s was saved", n)
		}
	}
}

// checkSafetyHeaders asserts the headers every response carries, downloads
// and refusals alike.
func checkSafetyHeaders(t *testing.T, resp *http.Response) {
	t.Helper()
	for k, v := range map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"X-Frame-Options":              "DENY",
		"X-Robots-Tag":                 "noindex, nofollow",
	} {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

// TestAdmissionBeforeFileSystem: refused requests are answered without the
// file system, which fails every access here.
func TestAdmissionBeforeFileSystem(t *testing.T) {
	s := newServer(t, Options{Path: makeShare(t, map[string]string{"a.txt": "a"}), Upload: true})
	s.fsys.Close()
	for _, c := range []struct {
		method, header, value string
		code                  int
	}{
		{"GET", "X-Tund-Auth", "", 403},
		{"GET", "X-Forwarded-For", "replay", 403},
		{"GET", "Sec-Fetch-Site", "cross-site", 403},
		{"PUT", "Origin", "https://evil.example", 403},
		{"DELETE", "", "", 405},
	} {
		r := request(c.method, "/a.txt", failRead{t})
		if c.header != "" {
			r.Header.Set(c.header, c.value)
		}
		if w := do(s, r); w.Code != c.code {
			t.Errorf("%s with %s=%q = %d, want %d", c.method, c.header, c.value, w.Code, c.code)
		}
	}
	if w := get(s, "/a.txt", ""); w.Code == http.StatusOK {
		t.Error("an admitted request didn't reach the closed file system")
	}
}

func TestAdmissionMessages(t *testing.T) {
	s := newServer(t, Options{Path: makeShare(t, map[string]string{"a.txt": "x"})})
	r := request("GET", "/a.txt", nil)
	r.Header.Del("X-Tund-Auth")
	if w := do(s, r); !strings.Contains(w.Body.String(), "passed its password or sign-in") {
		t.Errorf("no-auth body = %q", w.Body)
	}
	r = request("GET", "/a.txt", nil)
	r.Header.Set("X-Forwarded-For", "replay")
	r.Header.Set("Accept", "application/json")
	w := do(s, r)
	var v struct{ Error string }
	if json.Unmarshal(w.Body.Bytes(), &v); !strings.Contains(v.Error, "Replayed requests") {
		t.Errorf("replay body = %q", w.Body)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != dataCSP {
		t.Errorf("CSP = %q", got)
	}
	r = request("GET", "/a.txt", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Accept", "text/html")
	if w := do(s, r); w.Code != 403 || !strings.Contains(w.Body.String(), "came from another website") {
		t.Errorf("cross-site page = %d %q", w.Code, w.Body)
	}
}

func TestClosedServer(t *testing.T) {
	dir := makeShare(t, map[string]string{"a.txt": "x"})
	s := newServer(t, Options{Path: dir, Upload: true})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close on an idle server: %v", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	for _, m := range []string{"GET", "PUT"} {
		var body io.Reader
		if m == "PUT" {
			body = failRead{t}
		}
		w := do(s, request(m, "/a.txt", body))
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s after Close = %d", m, w.Code)
		}
		checkSafetyHeaders(t, w.Result())
	}
}

func TestRoot(t *testing.T) {
	dir := makeShare(t, map[string]string{"a.txt": "x"})
	s := newServer(t, Options{Path: dir})
	real, _ := filepath.EvalSymlinks(dir)
	if got := s.Root(); got != (Root{Path: real, Shown: tilde(real, homeDir()), Dir: true, Name: "share"}) {
		t.Errorf("Root() = %+v", got)
	}
	if _, err := New(Options{Path: filepath.Join(dir, "a.txt"), Upload: true}); err == nil ||
		!strings.HasPrefix(err.Error(), "--upload needs a folder") {
		t.Errorf("New(file, upload) = %v", err)
	}
	f := newServer(t, Options{Path: filepath.Join(dir, "a.txt")})
	if got := f.Root(); got.Dir || got.Name != "a.txt" {
		t.Errorf("file Root() = %+v", got)
	}
}

// fakeInfo is a FileInfo with only Sys, for the OS flag helpers.
type fakeInfo struct{ sys any }

func (f fakeInfo) Name() string       { return "x" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return 0 }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return f.sys }
