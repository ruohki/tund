package fileserve

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"tund/internal/secretfile"
)

// maxRows is how many rows an HTML listing renders (variable for tests).
var maxRows = 2000

// get answers GET and HEAD in a folder share: listings, the slash redirect
// and downloads.
func (s *Server) get(w http.ResponseWriter, r *http.Request, segs []string, slash bool) {
	rv := s.resolver()
	n, deepest, err := rv.resolve(segs)
	switch {
	case err != nil, !n.dir && slash:
		s.fail(w, r, s.notFound(pathText(segs, slash), &deepest))
	case n.dir && !slash:
		// Built by hand from the escaped segments: never http.Redirect, which
		// would resolve it against the decoded request path. The query stays
		// (?format=json, ?sort=); no-store, as the folder may become a file.
		loc := n.href()
		if r.URL.RawQuery != "" {
			loc += "?" + r.URL.RawQuery
		}
		h := w.Header()
		h.Set("Location", loc)
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusMovedPermanently)
	case n.dir:
		s.listing(w, r, rv, n)
	default:
		s.download(w, r, n, false)
	}
}

// serveFileShare answers a single-file share: "/" is the download page for
// browsers and the file itself for everyone else (curl -OJ <url>/ works),
// "/<name>" is the file, and nothing else exists.
func (s *Server) serveFileShare(w http.ResponseWriter, r *http.Request, segs []string, slash bool) {
	n, err := s.fileNode()
	switch {
	case err != nil:
		s.fail(w, r, s.notFound(pathText(segs, slash), nil))
	case len(segs) == 0 && formatOf(r) == formatHTML:
		s.filePage(w, r, n)
	case len(segs) == 0:
		s.download(w, r, n, true)
	case len(segs) == 1 && !slash && sameName(segs[0], n.name()):
		s.download(w, r, n, false)
	default:
		s.fail(w, r, s.notFound(pathText(segs, slash), nil))
	}
}

// sameName matches a URL segment against a real name: exactly, or in NFC.
func sameName(seg, name string) bool {
	return seg == name || utf8.ValidString(seg) && norm.NFC.String(seg) == norm.NFC.String(name)
}

// item is one visible entry of a listing.
type item struct {
	name string // the entry's own name in the folder (a link's, not its target's)
	key  string // Fold key, for sorting
	dir  bool
	size int64
	mod  time.Time
}

// listing answers a folder URL in HTML, JSON or text.
func (s *Server) listing(w http.ResponseWriter, r *http.Request, rv *resolver, dir node) {
	d, err := rv.list(dir.rel)
	if err != nil {
		s.fail(w, r, s.readProblem(s.changed(dir, err), "folder"))
		return
	}
	items := make([]item, 0, len(d.names))
	for _, name := range d.names {
		rv.hops = 0
		n, err := rv.entry(dir.rel, name)
		if err != nil || !n.dir && s.sniffListed(n, name) {
			continue
		}
		it := item{name: name, key: secretfile.Fold(name), dir: n.dir, mod: n.info.ModTime()}
		if !n.dir {
			it.size = n.info.Size()
		}
		items = append(items, it)
	}
	by, desc := sortOf(r)
	sortItems(items, by, desc)

	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Add("Vary", "Accept")
	switch formatOf(r) {
	case formatHTML:
		s.listPage(w, r, dir, items, d.capped, by, desc)
	case formatJSON:
		out := jsonListing{Share: s.root.Name, Path: dir.href(), Upload: s.upload, Total: len(items), Truncated: d.capped, Entries: []jsonEntry{}}
		for _, it := range items {
			e := jsonEntry{Name: it.name, Href: href(append(slices.Clip(dir.segs), it.name), it.dir), Type: "file",
				Kind: kindOf(it.name, it.dir), Modified: it.mod.UTC().Format(time.RFC3339)}
			if it.dir {
				e.Type = "folder"
			} else {
				e.Size = &it.size
			}
			out.Entries = append(out.Entries, e)
		}
		writeData(w, r, http.StatusOK, "application/json; charset=utf-8", jsonBytes(out))
	default:
		var b strings.Builder
		for _, it := range items {
			b.WriteString(it.name)
			if it.dir {
				b.WriteByte('/')
			}
			b.WriteByte('\n')
		}
		writeData(w, r, http.StatusOK, textType, []byte(b.String()))
	}
}

// jsonListing is the JSON form of a listing. href is authoritative: names may
// hold anything the OS allows (JSON turns invalid UTF-8 into U+FFFD).
type jsonListing struct {
	Share     string      `json:"share"`
	Path      string      `json:"path"` // escaped URL path of the folder
	Upload    bool        `json:"upload"`
	Total     int         `json:"total"`
	Truncated bool        `json:"truncated"` // the folder has more than maxScan entries
	Entries   []jsonEntry `json:"entries"`
}

type jsonEntry struct {
	Name     string `json:"name"`
	Href     string `json:"href"`
	Type     string `json:"type"` // "folder" or "file"
	Kind     string `json:"kind"`
	Size     *int64 `json:"size,omitempty"`
	Modified string `json:"modified"` // RFC 3339, UTC
}

func jsonBytes(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only plain structs, maps and strings get here
	}
	return append(b, '\n')
}

// writeData writes a JSON or text body that no browser may render as a page.
func writeData(w http.ResponseWriter, r *http.Request, status int, ctype string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Security-Policy", dataCSP)
	writeBody(w, r, status, body)
}

const dataCSP = "default-src 'none'; frame-ancestors 'none'; sandbox"

type format int

const (
	formatText format = iota
	formatHTML
	formatJSON
)

// formatOf negotiates listings and error bodies: ?format=json, then HTML for
// browsers, JSON for Accept: application/json, plain text for the rest.
func formatOf(r *http.Request) format {
	if r.URL.Query().Get("format") == "json" {
		return formatJSON
	}
	accept := r.Header.Get("Accept")
	switch {
	case strings.Contains(accept, "text/html"):
		return formatHTML
	case strings.Contains(accept, "application/json"):
		return formatJSON
	}
	return formatText
}

// sortOf reads ?sort=name|size|modified&order=asc|desc.
func sortOf(r *http.Request) (by string, desc bool) {
	q := r.URL.Query()
	switch by = q.Get("sort"); by {
	case "size", "modified":
	default:
		by = "name"
	}
	return by, q.Get("order") == "desc"
}

// sortItems puts folders first, then sorts by the column, in natural name
// order (img2 before img10) and then raw bytes to break ties.
func sortItems(items []item, by string, desc bool) {
	slices.SortStableFunc(items, func(a, b item) int {
		if a.dir != b.dir {
			if a.dir {
				return -1
			}
			return 1
		}
		c := 0
		switch by {
		case "size":
			c = cmp.Compare(a.size, b.size)
		case "modified":
			c = a.mod.Compare(b.mod)
		}
		if c == 0 {
			c = natural(a.key, b.key)
		}
		if c == 0 {
			c = strings.Compare(a.name, b.name)
		}
		if desc {
			c = -c
		}
		return c
	})
}

// natural compares runs of ASCII digits by value and everything else by rune.
func natural(a, b string) int {
	for a != "" && b != "" {
		if da, db := digits(a), digits(b); da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if c := cmp.Or(cmp.Compare(len(na), len(nb)), strings.Compare(na, nb), cmp.Compare(da, db)); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		ra, sa := utf8.DecodeRuneInString(a)
		rb, sb := utf8.DecodeRuneInString(b)
		if ra != rb {
			return cmp.Compare(ra, rb)
		}
		a, b = a[sa:], b[sb:]
	}
	return cmp.Compare(len(a), len(b))
}

func digits(s string) int {
	i := 0
	for i < len(s) && '0' <= s[i] && s[i] <= '9' {
		i++
	}
	return i
}

// kindExts maps lowercase extensions to kinds, which pick the icon, color and
// label of an entry.
var kindExts = func() map[string]string {
	m := map[string]string{}
	for kind, exts := range map[string]string{
		"image":   "png jpg jpeg gif webp avif heic heif bmp tif tiff svg ico psd cr2 nef arw dng",
		"video":   "mp4 m4v mov mkv webm avi wmv flv mpg mpeg 3gp",
		"audio":   "mp3 wav flac aac ogg oga opus m4a aif aiff wma mid midi",
		"pdf":     "pdf",
		"doc":     "doc docx odt rtf pages txt md markdown rst tex epub log",
		"sheet":   "xls xlsx xlsm ods csv tsv numbers",
		"slides":  "ppt pptx odp key",
		"archive": "zip tar gz tgz bz2 tbz2 xz txz zst 7z rar dmg iso pkg deb rpm apk jar war whl",
		"code": "js mjs cjs ts tsx jsx go py rb rs java kt swift c h cc cpp hpp cs php sh bash zsh fish ps1 bat sql " +
			"json yaml yml toml ini xml html htm css scss vue svelte lua pl r dart ex exs erl hs scala ipynb",
		"cert": "pub crt cer asc sig gpg",
	} {
		for _, e := range strings.Fields(exts) {
			m[e] = kind
		}
	}
	return m
}()

// kindOf returns an entry's kind: folder, image, video, audio, pdf, doc,
// sheet, slides, archive, code, cert or other.
func kindOf(name string, dir bool) string {
	if dir {
		return "folder"
	}
	lower := strings.ToLower(name)
	switch lower {
	case "dockerfile", "makefile":
		return "code"
	}
	for _, x := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst"} {
		if strings.HasSuffix(lower, x) {
			return "archive"
		}
	}
	if k, ok := kindExts[extOf(lower)]; ok {
		return k
	}
	return "other"
}

// kindLabel names a file's type for its page: "PNG image", "PDF document";
// "File" without an extension.
func kindLabel(name string) string {
	_, ext := splitExt(name)
	ext = strings.ToUpper(strings.TrimPrefix(ext, "."))
	if ext == "" {
		return "File"
	}
	noun := "file"
	switch kindOf(name, false) {
	case "image", "video", "audio", "archive":
		noun = kindOf(name, false)
	case "pdf", "doc":
		noun = "document"
	case "sheet":
		noun = "spreadsheet"
	case "slides":
		noun = "presentation"
	}
	return ext + " " + noun
}

// sniffKey identifies a listing sniff: the same file, name, size and mtime
// get the same verdict.
type sniffKey struct {
	rel, name string
	size      int64
	mtime     int64
}

// sniffCache remembers listing sniffs; it forgets everything when full.
type sniffCache struct {
	mu sync.Mutex
	m  map[sniffKey]bool
}

const sniffCacheMax = 4096

func (c *sniffCache) get(k sniffKey) (hit, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hit, ok = c.m[k]
	return hit, ok
}

func (c *sniffCache) put(k sniffKey, hit bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= sniffCacheMax {
		c.m = map[sniffKey]bool{}
	}
	c.m[k] = hit
}

// humanBytes formats n like the CLI: 1024-based, "2.4 MB".
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// count formats n with thousands separators: "12,345".
func count(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// baseExt splits a display name so pages can truncate it and keep the
// extension ("report-final-v3" + ".pdf").
func baseExt(name string, dir bool) (string, string) {
	if dir {
		return name, ""
	}
	return splitExt(name)
}
