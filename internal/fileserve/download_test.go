package fileserve

import (
	"bufio"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestContentTypes(t *testing.T) {
	cases := []struct {
		name, content, ctype, disp, csp string
	}{
		{"x.html", "<script>alert(1)</script>", binaryType, "attachment", attachmentCSP},
		{"x.htm", "<p>", binaryType, "attachment", attachmentCSP},
		{"x.shtml", "<p>", binaryType, "attachment", attachmentCSP},
		{"x.xhtml", "<p>", binaryType, "attachment", attachmentCSP},
		{"x.svg", "<svg onload=alert(1)>", binaryType, "attachment", attachmentCSP},
		{"x.svgz", "gz", binaryType, "attachment", attachmentCSP},
		{"x.xml", "<a/>", binaryType, "attachment", attachmentCSP},
		{"x.mht", "MIME", binaryType, "attachment", attachmentCSP},
		{"x.xyz", "data", binaryType, "attachment", attachmentCSP},
		{"noext", "<html><script>", binaryType, "attachment", attachmentCSP},
		{"x.png", "\x89PNG\r\n", "image/png", "inline", inlineCSP},
		{"x.JPG", "\xff\xd8", "image/jpeg", "inline", inlineCSP},
		{"x.webp", "RIFF", "image/webp", "inline", inlineCSP},
		{"x.mp4", "....ftyp", "video/mp4", "inline", inlineCSP},
		{"x.mp3", "ID3", "audio/mpeg", "inline", inlineCSP},
		{"x.pdf", "%PDF-1.4", "application/pdf", "inline", pdfCSP},
		{"x.txt", "hello", textType, "inline", inlineCSP},
		{"x.md", "<h1>hi</h1><script>alert(1)</script>", textType, "inline", inlineCSP},
		{"x.js", "alert(1)", textType, "inline", inlineCSP},
		{"x.csv", "a,b", textType, "inline", inlineCSP},
		{"empty.txt", "", textType, "inline", inlineCSP},
		{"bin.txt", "a\x00b", binaryType, "attachment", attachmentCSP},
		{"bad.json", "\xff\xfe{}", binaryType, "attachment", attachmentCSP},
	}
	files := map[string]string{}
	for _, c := range cases {
		files[c.name] = c.content
	}
	s := newServer(t, Options{Path: makeShare(t, files)})
	for _, c := range cases {
		w := get(s, "/"+c.name, "")
		h := w.Header()
		if w.Code != http.StatusOK || h.Get("Content-Type") != c.ctype || !strings.HasPrefix(h.Get("Content-Disposition"), c.disp+`; filename="`+c.name+`"`) ||
			h.Get("Content-Security-Policy") != c.csp || h.Get("Cache-Control") != "private, no-cache" || w.Body.String() != c.content {
			t.Errorf("GET %s = %d %q %q %q %q", c.name, w.Code, h.Get("Content-Type"), h.Get("Content-Disposition"), h.Get("Content-Security-Policy"), h.Get("Cache-Control"))
		}
		checkSafetyHeaders(t, w.Result())
		// ?download keeps the type from the table but never shows the file inline.
		w = get(s, "/"+c.name+"?download", "")
		if h := w.Header(); h.Get("Content-Type") != c.ctype || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment;") ||
			h.Get("Content-Security-Policy") != attachmentCSP {
			t.Errorf("GET %s?download = %q %q %q", c.name, h.Get("Content-Type"), h.Get("Content-Disposition"), h.Get("Content-Security-Policy"))
		}
	}
}

func TestDisposition(t *testing.T) {
	cases := []struct {
		inline bool
		name   string
		want   string
	}{
		{false, "report.pdf", `attachment; filename="report.pdf"`},
		{true, "a b;c=d.png", `inline; filename="a b;c=d.png"`},
		{false, "Über report.pdf", `attachment; filename="_ber report.pdf"; filename*=UTF-8''%C3%9Cber%20report.pdf`},
		{false, `a"b\c.txt`, `attachment; filename="a_b_c.txt"; filename*=UTF-8''a%22b%5Cc.txt`},
		{false, "tab\tname", `attachment; filename="tab_name"; filename*=UTF-8''tab%09name`},
		{false, "caf\xe9.txt", `attachment; filename="caf_.txt"; filename*=UTF-8''caf%E9.txt`},
		{false, "日本語.txt", `attachment; filename="___.txt"; filename*=UTF-8''%E6%97%A5%E6%9C%AC%E8%AA%9E.txt`},
		{false, "100%.txt", `attachment; filename="100%.txt"`},
	}
	for _, c := range cases {
		got := disposition(c.inline, c.name)
		if got != c.want {
			t.Errorf("disposition(%q) = %s\nwant %s", c.name, got, c.want)
		}
		if _, params, err := mime.ParseMediaType(got); err != nil || strings.ContainsRune(c.name, 0xFFFD) == false &&
			isUTF8(c.name) && params["filename"] != c.name {
			t.Errorf("ParseMediaType(%s) = %q, %v; want %q", got, params["filename"], err, c.name)
		}
	}
}

func isUTF8(s string) bool { return strings.ToValidUTF8(s, "") == s }

func TestRangesAndConditionals(t *testing.T) {
	dir := makeShare(t, map[string]string{"data.bin": "0123456789"})
	s := newServer(t, Options{Path: dir})
	w := do(s, request("HEAD", "/data.bin", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Length") != "10" || w.Body.Len() != 0 {
		t.Errorf("HEAD = %d %v %q", w.Code, w.Header(), w.Body)
	}
	etag := w.Header().Get("ETag")
	fi, _ := os.Stat(filepath.Join(dir, "data.bin"))
	if want := fmt.Sprintf(`"a-%x"`, fi.ModTime().UnixNano()); etag != want {
		t.Errorf("ETag = %s, want %s", etag, want)
	}
	r := request("GET", "/data.bin", nil)
	r.Header.Set("Range", "bytes=2-5")
	if w := do(s, r); w.Code != http.StatusPartialContent || w.Body.String() != "2345" || w.Header().Get("Content-Range") != "bytes 2-5/10" {
		t.Errorf("Range = %d %q %v", w.Code, w.Body, w.Header())
	}
	r.Header.Set("Range", "bytes=20-30")
	if w := do(s, r); w.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("bad Range = %d", w.Code)
	}
	r = request("GET", "/data.bin", nil)
	r.Header.Set("If-None-Match", etag)
	if w := do(s, r); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Errorf("If-None-Match = %d", w.Code)
	}
	r.Header.Set("If-None-Match", `W/`+etag)
	if w := do(s, r); w.Code != http.StatusNotModified {
		t.Errorf("weak If-None-Match = %d", w.Code)
	}
	// Rewritten within the same second: a new validator.
	os.Chtimes(filepath.Join(dir, "data.bin"), fi.ModTime(), fi.ModTime().Add(time.Millisecond))
	if w := do(s, r); w.Code != http.StatusOK || w.Header().Get("ETag") == etag {
		t.Errorf("after a change: %d %s", w.Code, w.Header().Get("ETag"))
	}
	r = request("GET", "/data.bin", nil)
	r.Header.Set("Range", "bytes=0-1")
	r.Header.Set("If-Range", etag)
	if w := do(s, r); w.Code != http.StatusOK || w.Body.String() != "0123456789" {
		t.Errorf("stale If-Range = %d %q", w.Code, w.Body)
	}
}

func TestSecretsAreNotServed(t *testing.T) {
	key := keyPEM(t)
	dir := makeShare(t, map[string]string{"notes.txt": "my key:\n" + key, "server.pem": key, "server.key": key, "cert.pem": certPEM,
		"id_card.jpg": "\xff\xd8jpeg", "big.log": strings.Repeat("x", 70<<10) + key})
	symlink(t, "server.pem", filepath.Join(dir, "innocent.txt"))
	s := newServer(t, Options{Path: dir})
	// Key-like names get a deep look, and listings hide what it finds: those
	// answer like missing paths. Other text files are listed; they say why.
	for p, code := range map[string]int{"/notes.txt": 403, "/server.pem": 404, "/server.key": 404, "/innocent.txt": 404} {
		for _, m := range []string{"GET", "HEAD"} {
			for _, rng := range []string{"", "bytes=0-10"} {
				r := request(m, p, nil)
				r.Header.Set("Range", rng)
				w := do(s, r)
				if w.Code != code || strings.Contains(w.Body.String(), "BEGIN") || w.Header().Get("Content-Disposition") != "" {
					t.Errorf("%s %s (%s) = %d %q, want %d", m, p, rng, w.Code, w.Body, code)
				}
			}
		}
		want := "isn&#39;t shared"
		if code == http.StatusNotFound {
			want = "There is no file or folder at"
		}
		if w := get(s, p, "text/html"); w.Code != code || !strings.Contains(w.Body.String(), want) {
			t.Errorf("GET %s page = %d %s", p, w.Code, w.Body)
		}
	}
	if names := listNames(t, s, "/"); !slices.Equal(names, []string{"big.log", "cert.pem", "id_card.jpg", "notes.txt"}) {
		t.Errorf("listing = %q", names)
	}
	for _, p := range []string{"/cert.pem", "/id_card.jpg", "/big.log"} {
		if w := get(s, p, ""); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d", p, w.Code)
		}
	}
}

// TestHiddenSecretsAnswerLikeMissing: a file that listings hide for what a
// deep sniff found in it answers exactly as it would if it didn't exist, in
// every format, so visitors can't tell the two apart.
func TestHiddenSecretsAnswerLikeMissing(t *testing.T) {
	key := keyPEM(t)
	dir := makeShare(t, map[string]string{"keys/server.key": key, "keys/ok.txt": "ok", "one/server.pem": certPEM})
	folder := newServer(t, Options{Path: dir})
	one := newServer(t, Options{Path: filepath.Join(dir, "one", "server.pem")})
	// The shared file of a single-file share turns into a key after the start.
	if err := os.WriteFile(filepath.Join(dir, "one", "server.pem"), []byte(key), 0o644); err != nil {
		t.Fatal(err)
	}
	nonce := regexp.MustCompile(`[0-9a-f]{32}`)
	type ask struct {
		s                      *Server
		method, target, accept string
	}
	asks := []ask{
		{folder, "GET", "/keys/server.key", ""}, {folder, "GET", "/keys/server.key", "application/json"},
		{folder, "GET", "/keys/server.key", "text/html"}, {folder, "HEAD", "/keys/server.key", ""},
		{folder, "GET", "/keys/server.key?download", ""},
		{one, "GET", "/", ""}, {one, "GET", "/", "text/html"}, {one, "GET", "/server.pem", ""},
	}
	answer := func(a ask) string {
		r := request(a.method, a.target, nil)
		r.Header.Set("Accept", a.accept)
		w := do(a.s, r)
		return nonce.ReplaceAllString(fmt.Sprint(w.Code, w.Header(), w.Body), "N") // pages have a nonce each
	}
	var hidden []string
	for _, a := range asks {
		hidden = append(hidden, answer(a))
	}
	os.Remove(filepath.Join(dir, "keys", "server.key"))
	os.Remove(filepath.Join(dir, "one", "server.pem"))
	for i, a := range asks {
		if missing := answer(a); hidden[i] != missing || !strings.HasPrefix(missing, "404 ") {
			t.Errorf("%s %s (%s):\nhidden  %s\nmissing %s", a.method, a.target, a.accept, hidden[i], missing)
		}
	}
}

// stalledConn is the share's end of a tunnel stream whose visitor left in
// the middle of a download: the edge closed its side, so reads end, while
// writes block until their deadline because nothing reads them any more.
type stalledConn struct {
	net.Conn
	gone chan struct{}
}

func (c *stalledConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	select {
	case <-c.gone:
		return n, io.EOF
	default:
		return n, err
	}
}

func (c *stalledConn) leave() {
	close(c.gone)
	c.Conn.SetReadDeadline(time.Now())
}

// oneConn hands a single connection to an http.Server.
type oneConn struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *oneConn) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *oneConn) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *oneConn) Addr() net.Addr { return &net.TCPAddr{} }

// TestAbortedDownloadEnds: a download whose visitor left ends at once, rather
// than holding the stream and the file until the edge gives up minutes later.
func TestAbortedDownloadEnds(t *testing.T) {
	dir := makeShare(t, map[string]string{"big.bin": strings.Repeat("x", 8<<20)})
	s := newServer(t, Options{Path: dir})
	ended := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(ended)
		s.ServeHTTP(w, r)
	})}
	client, server := net.Pipe()
	conn := &stalledConn{Conn: server, gone: make(chan struct{})}
	l := &oneConn{conns: make(chan net.Conn, 1), done: make(chan struct{})}
	l.conns <- conn
	go srv.Serve(l)
	defer func() {
		srv.Close()
		client.Close()
		select { // the file stays open until the handler returns
		case <-ended:
		case <-time.After(5 * time.Second):
		}
	}()

	go io.WriteString(client, "GET /big.bin HTTP/1.1\r\nHost: share.example\r\nX-Tund-Auth: password\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("download = %v, %v", resp, err)
	}
	if _, err := io.ReadFull(resp.Body, make([]byte, 64<<10)); err != nil {
		t.Fatal(err)
	}
	conn.leave()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the download still holds the stream after its visitor left")
	}
}

// TestKeepAlive: responses that end normally leave their connection usable;
// what ends the responses of visitors who left is gone once one returns.
func TestKeepAlive(t *testing.T) {
	srv := httptest.NewServer(newServer(t, Options{Path: makeShare(t, map[string]string{"a.txt": strings.Repeat("a", 100<<10)})}))
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(conn)
	for i := range 50 {
		io.WriteString(conn, "GET /a.txt HTTP/1.1\r\nHost: share.example\r\nX-Tund-Auth: password\r\n\r\n")
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		n, err := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err != nil || n != 100<<10 || resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d = %d, %d bytes, %v", i, resp.StatusCode, n, err)
		}
	}
}

func TestFileShare(t *testing.T) {
	dir := makeShare(t, map[string]string{"Q3 report.pdf": "%PDF-1.4", "other.txt": "o", ".env": "S=1"})
	s := newServer(t, Options{Path: filepath.Join(dir, "Q3 report.pdf")})
	if w := get(s, "/", ""); w.Code != http.StatusOK || w.Body.String() != "%PDF-1.4" ||
		w.Header().Get("Content-Disposition") != `attachment; filename="Q3 report.pdf"` || w.Header().Get("Content-Type") != "application/pdf" {
		t.Errorf("GET / (curl) = %d %v", w.Code, w.Header())
	}
	page := get(s, "/", "text/html")
	html := page.Body.String()
	for _, want := range []string{"<title>Shared file</title>",
		`<p class="title"><span class="nm" dir="ltr"><span class="base" dir="auto">Q3 report</span><span class="ext" dir="ltr">.pdf</span></span></p>`,
		`href="/Q3%20report.pdf?download"`, `href="/Q3%20report.pdf"`, "PDF document · 8 B", `curl -u share -OJ &#39;https://share.example/Q3%20report.pdf&#39;`} {
		if !strings.Contains(html, want) {
			t.Errorf("file page lacks %s", want)
		}
	}
	if page.Code != http.StatusOK || page.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("file page = %d %v", page.Code, page.Header())
	}
	if w := get(s, "/Q3%20report.pdf", ""); w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline;") {
		t.Errorf("GET /Q3 report.pdf = %d %v", w.Code, w.Header())
	}
	if w := get(s, "/Q3%20report.pdf?download", ""); !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Errorf("?download = %v", w.Header())
	}
	for _, p := range []string{"/other.txt", "/.env", "/Q3%20report.pdf/", "/x/Q3%20report.pdf", "/q3%20report.pdf", "//Q3%20report.pdf"} {
		r := request("GET", "", nil)
		r.URL.RawPath, r.URL.Path = p, mustUnescape(p)
		if w := do(s, r); w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d", p, w.Code)
		}
	}
	w := get(s, "/nope", "text/html")
	if !strings.Contains(w.Body.String(), `<a class="btn" href="/" dir="auto">Go to Q3 report.pdf</a>`) {
		t.Errorf("404 page:\n%s", w.Body)
	}
	if w := do(s, request("PUT", "/x.txt", failRead{t})); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT = %d", w.Code)
	}
	// The file is checked again on every request.
	os.WriteFile(filepath.Join(dir, "Q3 report.pdf"), []byte(keyPEM(t)), 0o644)
	for _, accept := range []string{"", "text/html"} {
		if w := get(s, "/", accept); w.Code != http.StatusForbidden {
			t.Errorf("GET / (%q) of a secret = %d", accept, w.Code)
		}
	}
	os.Remove(filepath.Join(dir, "Q3 report.pdf"))
	for _, accept := range []string{"", "text/html"} {
		if w := get(s, "/", accept); w.Code != http.StatusNotFound {
			t.Errorf("GET / (%q) of a deleted file = %d", accept, w.Code)
		}
	}
}

func TestFileSharePreview(t *testing.T) {
	dir := makeShare(t, map[string]string{"photo.png": "\x89PNG", "huge.png": "", "vector.svg": "<svg/>"})
	os.Truncate(filepath.Join(dir, "huge.png"), 11<<20)
	for name, preview := range map[string]bool{"photo.png": true, "huge.png": false, "vector.svg": false} {
		s := newServer(t, Options{Path: filepath.Join(dir, name)})
		html := get(s, "/", "text/html").Body.String()
		if got := strings.Contains(html, `<img class="preview" src="/`+name+`"`); got != preview {
			t.Errorf("%s: preview = %v", name, got)
		}
		if inline := strings.Contains(html, ">Open</a>"); inline != (name != "vector.svg") {
			t.Errorf("%s: Open = %v", name, inline)
		}
	}
}
