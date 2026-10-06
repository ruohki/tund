package fileserve

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestCompression(t *testing.T) {
	files := map[string]string{"big.txt": strings.Repeat("plain text ", 1000), "sub/": ""}
	for i := range 100 {
		files[fmt.Sprintf("file-%03d.txt", i)] = "x"
	}
	s := newServer(t, Options{Path: makeShare(t, files)})
	nonce := regexp.MustCompile(`[0-9a-f]{32}`) // every page has its own
	ask := func(method, target, accept, encoding string) *http.Response {
		r := request(method, target, nil)
		r.Header.Set("Accept", accept)
		r.Header.Set("Accept-Encoding", encoding)
		return do(s, r).Result()
	}
	varies := func(resp *http.Response) bool { return slices.Contains(resp.Header.Values("Vary"), "Accept-Encoding") }

	for _, c := range []struct{ name, target, accept string }{
		{"page", "/", "text/html"},
		{"JSON", "/", "application/json"},
		{"text", "/", ""},
		{"error page", "/nope", "text/html"},
	} {
		plain := ask("GET", c.target, c.accept, "")
		want, _ := io.ReadAll(plain.Body)
		if plain.Header.Get("Content-Encoding") != "" || !varies(plain) {
			t.Errorf("%s without gzip: %v", c.name, plain.Header)
		}
		resp := ask("GET", c.target, c.accept, "br, gzip;q=0.8")
		packed, _ := io.ReadAll(resp.Body)
		if resp.Header.Get("Content-Encoding") != "gzip" || !varies(resp) || resp.Header.Get("Content-Length") != fmt.Sprint(len(packed)) ||
			len(packed) >= len(want) {
			t.Errorf("%s with gzip: %d of %d bytes, %v", c.name, len(packed), len(want), resp.Header)
			continue
		}
		zr, err := gzip.NewReader(bytes.NewReader(packed))
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(zr)
		if err != nil || nonce.ReplaceAllString(string(got), "N") != nonce.ReplaceAllString(string(want), "N") {
			t.Errorf("%s: gzip holds something else (%v)", c.name, err)
		}
		// HEAD: the headers GET would send, and no body.
		head := ask("HEAD", c.target, c.accept, "gzip")
		if body, _ := io.ReadAll(head.Body); head.Header.Get("Content-Encoding") != "gzip" || len(body) != 0 ||
			c.accept != "text/html" && head.Header.Get("Content-Length") != fmt.Sprint(len(packed)) {
			t.Errorf("%s HEAD: %d bytes, %v", c.name, len(body), head.Header)
		}
	}

	// Shared files are sent as they are, with their ranges.
	resp := ask("GET", "/big.txt", "", "gzip")
	if body, _ := io.ReadAll(resp.Body); resp.Header.Get("Content-Encoding") != "" || varies(resp) || string(body) != files["big.txt"] {
		t.Errorf("a download was compressed: %v", resp.Header)
	}
	// Small bodies aren't worth it, and don't vary.
	if resp := ask("GET", "/sub/", "", "gzip"); resp.Header.Get("Content-Encoding") != "" || varies(resp) {
		t.Errorf("a small listing was compressed: %v", resp.Header)
	}
	// Refused encodings.
	for _, enc := range []string{"gzip;q=0", "br", "identity", "gzip;q=0, *"} {
		if resp := ask("GET", "/", "application/json", enc); resp.Header.Get("Content-Encoding") != "" {
			t.Errorf("Accept-Encoding %q got %v", enc, resp.Header)
		}
	}
}

func TestAcceptsGzip(t *testing.T) {
	for v, want := range map[string]bool{
		"": false, "gzip": true, "GZip": true, "x-gzip": true, "gzip, deflate, br, zstd": true, "deflate,gzip": true,
		"gzip;q=0": false, "gzip; q=0.001": true, "gzip;Q=0": false, "gzip;q=0.0": false, "gzip ; q=1.0": true,
		"br": false, "identity": false, "*": true, "*;q=0": false, "br, *;q=0.1": true, "gzip;q=0, *": false,
		"gzip;q=bogus": false, "gzip;q=NaN": false, "gzip;q=2": false, "gzip;level=1": true,
	} {
		if got := acceptsGzip([]string{v}); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", v, got, want)
		}
	}
	if !acceptsGzip([]string{"br", "gzip"}) || acceptsGzip([]string{"gzip;q=0", "br"}) {
		t.Error("Accept-Encoding over several lines")
	}
}
