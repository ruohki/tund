package fileserve

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"tund/internal/secretfile"
)

// inlineTypes are the only files browsers may show inline: raster images,
// audio, video and PDF. The table is fixed on purpose: the OS MIME database
// differs between machines, and ServeContent's sniffing can say text/html.
var inlineTypes = map[string]string{
	"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp",
	"avif": "image/avif", "bmp": "image/bmp", "ico": "image/x-icon",
	"mp3": "audio/mpeg", "m4a": "audio/mp4", "aac": "audio/aac", "ogg": "audio/ogg", "oga": "audio/ogg",
	"opus": "audio/ogg", "wav": "audio/wav", "flac": "audio/flac", "weba": "audio/webm",
	"mp4": "video/mp4", "m4v": "video/mp4", "webm": "video/webm", "ogv": "video/ogg", "mov": "video/quicktime",
	"pdf": "application/pdf",
}

// textTypes are shown as plain UTF-8 text when their first 8 KiB are valid
// UTF-8 without NUL bytes. HTML, SVG, XML and everything else download.
var textTypes = setOf("txt md markdown log csv tsv json yaml yml toml ini cfg conf go py rb rs java kt c h cpp cs js ts css sh sql")

const (
	textType   = "text/plain; charset=utf-8"
	binaryType = "application/octet-stream"

	inlineCSP     = "default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'; sandbox"
	pdfCSP        = "frame-ancestors 'none'" // PDF viewers break in a sandbox and don't run the origin's script
	attachmentCSP = "default-src 'none'; sandbox"
)

func setOf(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

// extOf returns the lowercase extension of name without the dot.
func extOf(name string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
}

// contentType returns a file's Content-Type and whether browsers may show it
// inline.
func contentType(name string, f io.ReaderAt) (string, bool) {
	ext := extOf(name)
	if t, ok := inlineTypes[ext]; ok {
		return t, true
	}
	if textTypes[ext] && looksText(f) {
		return textType, true
	}
	return binaryType, false
}

// mayInline reports whether opening a file of this name may show it in the
// browser (text files are checked when they are served).
func mayInline(name string) bool {
	ext := extOf(name)
	return inlineTypes[ext] != "" || textTypes[ext]
}

// looksText reports whether the first 8 KiB are valid UTF-8 without NUL.
func looksText(f io.ReaderAt) bool {
	buf := make([]byte, 8<<10)
	n, err := f.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return false
	}
	b := buf[:n]
	if bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	if n == len(buf) { // the sample may end inside a character
		for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
			if utf8.RuneStart(b[i]) {
				if !utf8.FullRune(b[i:]) {
					b = b[:i]
				}
				break
			}
		}
	}
	return utf8.Valid(b)
}

// disposition builds Content-Disposition: an ASCII filename= that curl -OJ
// reads (non-ASCII, control characters, quotes and backslashes become "_"),
// plus RFC 5987 filename* for names that need it.
func disposition(inline bool, name string) string {
	var b strings.Builder
	if inline {
		b.WriteString(`inline; filename="`)
	} else {
		b.WriteString(`attachment; filename="`)
	}
	for _, r := range name {
		if notPlain(r) {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	if !plainName(name) {
		b.WriteString("; filename*=UTF-8''")
		for i := 0; i < len(name); i++ {
			if c := name[i]; attrChar(c) {
				b.WriteByte(c)
			} else {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
	}
	return b.String()
}

// plainName reports whether filename= carries a name as it is, so curl -OJ
// saves it under that name.
func plainName(name string) bool { return !strings.ContainsFunc(name, notPlain) }

// notPlain reports what filename= can't carry: control characters, quotes,
// backslashes and everything outside ASCII.
func notPlain(r rune) bool { return r < 0x20 || r >= 0x7f || r == '"' || r == '\\' }

// attrChar reports RFC 5987 attr-char bytes, which need no escaping.
func attrChar(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}

// etag is a strong validator from size and mtime in nanoseconds, so a file
// rewritten within the same second still revalidates.
func etag(fi fs.FileInfo) string {
	return fmt.Sprintf(`"%x-%x"`, uint64(fi.Size()), uint64(fi.ModTime().UnixNano()))
}

// download serves a resolved file. Range, HEAD and conditional requests are
// http.ServeContent's; Content-Type is always set first, so it never sniffs.
func (s *Server) download(w http.ResponseWriter, r *http.Request, n node, attach bool) {
	f, fi, err := s.open(n)
	if err != nil {
		s.fail(w, r, s.readProblem(err, "file"))
		return
	}
	defer f.Close()
	name := n.name()
	if p := s.sniffServed(r, n, f, fi.Size()); p != nil {
		s.fail(w, r, p)
		return
	}
	ctype, inline := contentType(name, f)
	if attach || r.URL.Query().Has("download") {
		inline = false
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", disposition(inline, name))
	h.Set("ETag", etag(fi))
	h.Set("Cache-Control", "private, no-cache")
	switch {
	case !inline:
		h.Set("Content-Security-Policy", attachmentCSP)
	case ctype == "application/pdf":
		h.Set("Content-Security-Policy", pdfCSP)
	default:
		h.Set("Content-Security-Policy", inlineCSP)
	}
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// sniffServed looks for secret content in the opened file f of node n before
// any byte of it is sent: deeply for key-like names (the link's or the
// target's), else small text. Listings hide deep hits, so those answer like
// a missing path; text hits are listed and get the 403 that says why.
func (s *Server) sniffServed(r *http.Request, n node, f *os.File, size int64) *problem {
	kind, which := sniffKind(size, n.name(), path.Base(n.rel))
	if kind == secretfile.SniffNone {
		return nil
	}
	_, found, err := secretfile.Sniff(f, size, which, kind)
	switch {
	case err != nil:
		return s.readProblem(err, "file")
	case found && kind == secretfile.SniffDeep:
		return s.missing(r, n)
	case found:
		return errSecret()
	}
	return nil
}

// missing answers for a file that isn't shared after all exactly as for a
// path that doesn't exist: the same 404, message and way back.
func (s *Server) missing(r *http.Request, n node) *problem {
	segs, slash, _ := splitPath(r.URL.EscapedPath())
	return s.notFound(pathText(segs, slash), &node{segs: n.segs[:len(n.segs)-1], dir: true})
}

// sniffKind picks the deepest sniff that any of a file's names (a link's and
// its target's) asks for, and the name that asked.
func sniffKind(size int64, names ...string) (secretfile.SniffKind, string) {
	kind, which := secretfile.SniffNone, ""
	for _, name := range names {
		switch k := secretfile.SniffFor(name, size); {
		case k == secretfile.SniffDeep:
			return k, name
		case k == secretfile.SniffText && kind == secretfile.SniffNone:
			kind, which = k, name
		}
	}
	return kind, which
}

// sniffListed reports whether a listed file must be hidden because a deep
// sniff found a secret in it. Verdicts are cached by path, size and mtime;
// cloud placeholders are never read, and errors leave the file listed (it
// can't be read for a download either).
func (s *Server) sniffListed(n node, name string) bool {
	size := n.info.Size()
	which := name
	if secretfile.SniffFor(which, size) != secretfile.SniffDeep {
		if which = path.Base(n.rel); secretfile.SniffFor(which, size) != secretfile.SniffDeep {
			return false
		}
	}
	if placeholder(n.info) {
		return false
	}
	key := sniffKey{n.rel, which, size, n.info.ModTime().UnixNano()}
	if hit, ok := s.sniffs.get(key); ok {
		return hit
	}
	f, fi, err := s.open(n)
	if err != nil {
		return false
	}
	defer f.Close()
	_, found, err := secretfile.Sniff(f, fi.Size(), which, secretfile.SniffDeep)
	if err != nil {
		return false
	}
	s.sniffs.put(key, found)
	return found
}
