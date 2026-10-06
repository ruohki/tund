package fileserve

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// minGzip is the smallest body worth compressing. Folder pages with uploads
// are about 65 KB and a listing of 2,000 rows more than 1 MB; most of it
// compresses away.
const minGzip = 1 << 10

var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}

// writeBody writes a body the server made (pages, listings and messages;
// never a shared file, which keeps its bytes and Range support): gzipped
// when it has at least minGzip bytes and the request accepts gzip.
// Content-Length is what is sent, so HEAD gets the headers GET would.
func writeBody(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	h := w.Header()
	if len(body) >= minGzip {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r.Header.Values("Accept-Encoding")) {
			body = gzipped(body)
			h.Set("Content-Encoding", "gzip")
		}
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

func gzipped(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzipWriters.Get().(*gzip.Writer)
	zw.Reset(&buf)
	zw.Write(b) // a bytes.Buffer takes everything
	zw.Close()
	gzipWriters.Put(zw)
	return buf.Bytes()
}

// acceptsGzip reads Accept-Encoding: gzip (or x-gzip), else "*", with a
// q-value above 0.
func acceptsGzip(accept []string) bool {
	gz, star := -1.0, -1.0
	for _, v := range accept {
		for _, e := range strings.Split(v, ",") {
			coding, params, _ := strings.Cut(e, ";")
			q := 1.0
			for _, p := range strings.Split(params, ";") {
				if k, val, ok := strings.Cut(strings.TrimSpace(p), "="); ok && strings.EqualFold(strings.TrimSpace(k), "q") {
					var err error
					if q, err = strconv.ParseFloat(strings.TrimSpace(val), 64); err != nil || !(q >= 0 && q <= 1) {
						q = 0
					}
				}
			}
			switch strings.ToLower(strings.TrimSpace(coding)) {
			case "gzip", "x-gzip":
				gz = max(gz, q)
			case "*":
				star = max(star, q)
			}
		}
	}
	if gz >= 0 {
		return gz > 0
	}
	return star > 0
}
