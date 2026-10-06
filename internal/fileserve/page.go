package fileserve

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The HTML pages render the view-model below with the templates in
// templates/: base.html defines "page" (the document, with the shared
// pieces), and list.html, file.html and error.html define its "body". Each
// page inlines its stylesheet, script and icons from assets/ (see parsePage),
// so it makes no other requests and works without the script. Both may change
// for the look, but every page must keep the rules TestPages checks, because
// the edge's phishing scanner reads pages and visitors' names are untrusted:
//   - <title>, <h1> and <h2> hold fixed text only, never a share, folder or
//     file name (those go in non-heading elements such as <p class="title">);
//   - no product name, no phishing phrases ("private key", "verify"...), no
//     <input type=password>, forms post to same-origin absolute paths;
//   - every <script> and <style> carries nonce="{{.Nonce}}"; no style=""
//     attributes and no on* handlers (the CSP blocks them anyway);
//   - names reach the page only through html/template escaping, in elements
//     with dir="auto"; a file name sits in an LTR box (class "nm") that
//     isolates only its stem, so a right-to-left stem can't move the real
//     extension. The view-model already shows bidi marks and control
//     characters as \uXXXX. The embedded assets are the only typed
//     (unescaped) content;
//   - links are the absolute, escaped paths from the view-model;
//   - the script builds the DOM with createElement and textContent, never
//     innerHTML, and escapes names from the JSON listing like the view-model.

// page is what every page has.
type page struct {
	Nonce    string   // CSP nonce for every <script> and <style>
	Title    string   // fixed <title>: "Shared folder", "Shared file" or "<Reason> · Shared folder"
	Eyebrow  string   // "Shared folder" or "Shared file"
	Share    string   // the share root's base name
	Host     string   // the request's host, for "Served directly from the owner's computer · <host>"
	Identity identity // who is signed in, from the edge's headers on this request
	Upload   bool     // visitors may upload to folders of this share
}

// identity is the visitor as the edge describes them on each request.
type identity struct {
	Method   string // "password" or "oidc"; "" on pages for requests that weren't signed in
	Email    string
	Name     string
	Username string
	Label    string // "Signed in with password", else the email, name, username or "Signed in"; "" without Method
	Initial  string // oidc: the avatar's letter, from the name, email or username
}

// listPage is a folder.
type listPage struct {
	page
	Crumbs    []crumb   // the share root first; the last one is the current folder
	Current   string    // the current folder's name (the share's at the root)
	Path      string    // the folder's escaped URL path, ending in "/": the upload form's action
	Entries   []entry   // at most maxRows, folders first
	Folders   int       // visible folders
	Files     int       // visible files
	Total     int       // Folders + Files
	Shown     int       // len(Entries)
	Truncated bool      // Shown < Total, or the folder has more than maxScan entries
	Notice    string    // "Showing the first 2,000 of 12,345 items." when truncated
	Meta      string    // "3 folders · 9 files" or "Empty folder"
	Sort      string    // active column: name, size or modified
	Order     string    // asc or desc
	Columns   []column  // header links for sorting without JavaScript
	Flash     string    // after a no-JS upload: "2 files uploaded. 1 was renamed because ..."
	Curl      []curlCmd // password shares only
}

// crumb is a breadcrumb or another link to a folder.
type crumb struct {
	Name string
	Href string
	Here bool // the current folder (aria-current)
}

// entry is a listing row.
type entry struct {
	Name          string // display form
	Base, Ext     string // Name split so truncation keeps the extension ("report" + ".pdf")
	Href          string // absolute, escaped; folders end in "/"
	DownloadHref  string // files: Href + "?download"
	IsDir         bool
	Kind          string // folder image video audio pdf doc sheet slides archive code cert other
	Size          int64  // files only
	SizeText      string // "2.4 MB"; "—" for folders
	ModifiedISO   string // RFC 3339, UTC
	ModifiedText  string // "6 Oct 2026" (UTC)
	ModifiedTitle string // "2026-10-06 12:03 UTC"
	Inline        bool   // opening Href may show it in the browser instead of downloading
}

// column is a sortable column header.
type column struct {
	Key   string // name, size or modified
	Label string
	Href  string // this folder sorted by the column; toggles the order when active
	Sort  string // aria-sort: ascending, descending or none
}

// curlCmd is a copyable command for password shares; curl asks for the
// password, so it is never printed.
type curlCmd struct {
	Comment string
	Command string
}

// filePage is the download page of a single-file share.
type filePage struct {
	page
	Name          string
	Base, Ext     string // Name split like a listing row's, for the same LTR name box
	Href          string // the file, inline when its type allows
	DownloadHref  string
	Kind          string
	KindLabel     string // "PDF document"
	Size          int64
	SizeText      string
	ModifiedISO   string
	ModifiedText  string
	ModifiedTitle string
	Inline        bool // offer "Open"
	Preview       bool // an image of at most 10 MB: show it
	Curl          []curlCmd
}

// errorPage is any answer that isn't the content asked for.
type errorPage struct {
	page
	Status  int
	Code    string // "404", "405 · uploads off"
	Heading string // fixed text
	Message string // may name a path or file
	// Message split around the path it names, which pages set apart as code:
	// Before + Subject + After == Message; all empty when it names none.
	Before, Subject, After string
	Detail                 string
	Foot                   string
	Actions                []action
}

type action struct {
	Label   string
	Href    string
	Primary bool
	Icon    string // a sprite icon before the label, or ""
}

//go:embed templates assets
var templateFS embed.FS

var templates = map[string]*template.Template{
	"list":   parsePage("list", "list.css list.js"),
	"upload": parsePage("list", "list.css upload.css list.js upload.js"), // a folder visitors may upload to
	"file":   parsePage("file", "solo.css"),
	"error":  parsePage("error", "solo.css"),
}

// parsePage parses the template of a page with the functions that inline its
// assets: base.css and base.js, then its own. Pages carry only what they use:
// the upload client is on folder pages of shares that accept uploads.
func parsePage(name, assets string) *template.Template {
	style, script := asset("base.css"), asset("base.js")
	for _, a := range strings.Fields(assets) {
		if strings.HasSuffix(a, ".css") {
			style += asset(a)
		} else {
			script += asset(a)
		}
	}
	css := template.CSS(compactCSS(style))
	// One strict function, so the files share their helpers and leave no
	// globals behind.
	js := template.JS("(() => {\n'use strict';\n" + compactJS(script) + "\n})();")
	sprite := template.HTML(icons(asset("icons.svg")))
	return template.Must(template.New("").Funcs(template.FuncMap{
		"css":    func() template.CSS { return css },
		"script": func() template.JS { return js },
		"sprite": func() template.HTML { return sprite },
		"icon":   kindIcon,
		"plural": plural,
	}).ParseFS(templateFS, "templates/base.html", "templates/"+name+".html"))
}

func asset(name string) string {
	b, err := templateFS.ReadFile("assets/" + name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// compactCSS drops the comments and line breaks of a stylesheet.
func compactCSS(css string) string {
	var b strings.Builder
	for {
		start := strings.Index(css, "/*")
		end := strings.Index(css[max(start, 0):], "*/")
		if start < 0 || end < 0 {
			break
		}
		b.WriteString(css[:start])
		css = css[start+end+2:]
	}
	b.WriteString(css)
	return strings.ReplaceAll(b.String(), "\n", "")
}

// compactJS drops the indentation, blank lines and whole-line comments of a
// script. Line breaks stay, so semicolon insertion works as written; the
// scripts have no multi-line strings or template literals.
func compactJS(js string) string {
	var b strings.Builder
	for line := range strings.Lines(js) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "//") {
			b.WriteString(line + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// icons is the sprite without its license header (the file keeps it) and
// with a credit instead.
func icons(svg string) string {
	return "<!-- Icons: Lucide (ISC License) -->" + strings.TrimSpace(svg[strings.Index(svg, "<svg"):])
}

// kindIcons are the sprite's icons for entry kinds; list.js has a copy.
var kindIcons = map[string]string{"folder": "folder", "image": "file-image", "video": "file-play",
	"audio": "file-headphone", "pdf": "file-text", "doc": "file-text", "sheet": "file-spreadsheet",
	"slides": "presentation", "archive": "file-archive", "code": "file-code", "cert": "file-key"}

func kindIcon(kind string) string { return cmp.Or(kindIcons[kind], "file") }

const pageCSP = "default-src 'none'; script-src 'nonce-%[1]s'; style-src 'nonce-%[1]s'; img-src 'self' data:; " +
	"media-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// base fills what every page has, for this request.
func (s *Server) base(r *http.Request, reason string) page {
	var nonce [16]byte
	rand.Read(nonce[:])
	p := page{
		// Hex, not base64: html/template escapes nothing in it, and random
		// letters can't spell what the edge's phishing heuristics count ("cvv").
		Nonce:   hex.EncodeToString(nonce[:]),
		Eyebrow: "Shared folder",
		Share:   shown(s.root.Name),
		Host:    shown(r.Host),
		Upload:  s.upload && s.root.Dir,
	}
	if !s.root.Dir {
		p.Eyebrow = "Shared file"
	}
	p.Title = p.Eyebrow
	if reason != "" {
		p.Title = reason + " · " + p.Eyebrow
	}
	id := &p.Identity
	switch id.Method = r.Header.Get("X-Tund-Auth"); id.Method {
	case "oidc":
		id.Email = shown(r.Header.Get("X-Tund-User-Email"))
		id.Name = shown(r.Header.Get("X-Tund-User-Name"))
		id.Username = shown(r.Header.Get("X-Tund-User-Username"))
		id.Label = cmp.Or(id.Email, id.Name, id.Username, "Signed in")
		if c, _ := utf8.DecodeRuneInString(cmp.Or(id.Name, id.Email, id.Username)); unicode.IsLetter(c) || unicode.IsDigit(c) {
			id.Initial = strings.ToUpper(string(c))
		}
	case "password":
		id.Label = "Signed in with password"
	default: // refused before admission: nobody is signed in
		id.Method = ""
	}
	return p
}

// render writes an HTML page with its own CSP nonce. (ServeHTTP sets
// Cross-Origin-Opener-Policy on every response.)
func render(w http.ResponseWriter, r *http.Request, status int, name, nonce string, data any) {
	var buf bytes.Buffer
	if err := templates[name].ExecuteTemplate(&buf, "page", data); err != nil {
		writeData(w, r, http.StatusInternalServerError, textType, []byte("Something went wrong.\n"))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", fmt.Sprintf(pageCSP, nonce))
	h.Set("Cache-Control", "no-store")
	if !slices.Contains(h.Values("Vary"), "Accept") {
		h.Add("Vary", "Accept")
	}
	writeBody(w, r, status, buf.Bytes())
}

// listPage renders a folder.
func (s *Server) listPage(w http.ResponseWriter, r *http.Request, dir node, items []item, capped bool, by string, desc bool) {
	p := listPage{page: s.base(r, ""), Path: dir.href(), Sort: by, Order: "asc", Total: len(items)}
	if desc {
		p.Order = "desc"
	}
	p.Crumbs = []crumb{{Name: p.Share, Href: "/"}}
	for i, seg := range dir.segs {
		p.Crumbs = append(p.Crumbs, crumb{Name: shown(seg), Href: href(dir.segs[:i+1], true)})
	}
	p.Crumbs[len(p.Crumbs)-1].Here = true
	p.Current = p.Crumbs[len(p.Crumbs)-1].Name
	for _, it := range items {
		if it.dir {
			p.Folders++
		} else {
			p.Files++
		}
		if len(p.Entries) == maxRows {
			continue
		}
		e := entry{Name: shown(it.name), IsDir: it.dir, Kind: kindOf(it.name, it.dir), SizeText: "—",
			Href: href(append(slices.Clip(dir.segs), it.name), it.dir)}
		e.Base, e.Ext = baseExt(e.Name, it.dir)
		e.ModifiedISO, e.ModifiedText, e.ModifiedTitle = times(it.mod)
		if !it.dir {
			e.DownloadHref = e.Href + "?download"
			e.Size, e.SizeText = it.size, humanBytes(it.size)
			e.Inline = mayInline(it.name)
		}
		p.Entries = append(p.Entries, e)
	}
	p.Shown = len(p.Entries)
	if p.Truncated = p.Shown < p.Total || capped; p.Truncated {
		total := count(p.Total)
		if capped {
			total = "more than " + total
		}
		p.Notice = fmt.Sprintf("Showing the first %s of %s items.", count(p.Shown), total)
	}
	p.Meta = meta(p.Folders, p.Files)
	for _, c := range [][2]string{{"name", "Name"}, {"size", "Size"}, {"modified", "Modified"}} {
		col := column{Key: c[0], Label: c[1], Sort: "none", Href: p.Path + "?sort=" + c[0] + "&order=asc"}
		if c[0] == by {
			col.Sort = "ascending"
			if desc {
				col.Sort = "descending"
			} else {
				col.Href = p.Path + "?sort=" + c[0] + "&order=desc"
			}
		}
		p.Columns = append(p.Columns, col)
	}
	q := r.URL.Query()
	if n, err := strconv.Atoi(q.Get("uploaded")); err == nil && n > 0 {
		k, _ := strconv.Atoi(q.Get("renamed"))
		p.Flash = flash(n, max(k, 0))
	}
	if p.Identity.Method == "password" {
		base := origin(r)
		p.Curl = append(p.Curl, curlCmd{"list this folder", "curl -u share " + shellQuote(base+p.Path)})
		for i, e := range p.Entries { // the rows are the first items
			if !e.IsDir {
				p.Curl = append(p.Curl, curlCmd{saveLabel("a file", items[i].name), "curl -u share -OJ " + shellQuote(base+e.Href)})
				break
			}
		}
		if p.Upload {
			p.Curl = append(p.Curl, curlCmd{"upload a file into this folder", "curl -u share -T file " + shellQuote(base+p.Path)})
		}
	}
	name := "list"
	if p.Upload {
		name = "upload"
	}
	render(w, r, http.StatusOK, name, p.Nonce, p)
}

// filePage renders the download page of a single-file share, after the same
// checks as the download itself.
func (s *Server) filePage(w http.ResponseWriter, r *http.Request, n node) {
	f, fi, err := s.open(n)
	if err != nil {
		s.fail(w, r, s.readProblem(err, "file"))
		return
	}
	pr := s.sniffServed(r, n, f, fi.Size())
	f.Close()
	if pr != nil {
		s.fail(w, r, pr)
		return
	}
	name := n.name()
	p := filePage{page: s.base(r, ""), Name: shown(name), Href: href(n.segs, false), Kind: kindOf(name, false),
		KindLabel: kindLabel(name), Size: fi.Size(), SizeText: humanBytes(fi.Size()), Inline: mayInline(name)}
	p.DownloadHref = p.Href + "?download"
	p.Base, p.Ext = baseExt(p.Name, false)
	p.ModifiedISO, p.ModifiedText, p.ModifiedTitle = times(fi.ModTime())
	_, image := inlineTypes[extOf(name)]
	p.Preview = image && p.Kind == "image" && fi.Size() <= 10<<20
	if p.Identity.Method == "password" {
		p.Curl = []curlCmd{{saveLabel("the file", name), "curl -u share -OJ " + shellQuote(origin(r)+p.Href)}}
	}
	render(w, r, http.StatusOK, "file", p.Nonce, p)
}

// saveLabel describes a curl -OJ snippet for a file of this name: curl saves
// it under Content-Disposition's ASCII filename=, which has "_" for the rest.
func saveLabel(what, name string) string {
	if plainName(name) {
		return "download " + what + ", keeping its name"
	}
	return "download " + what + " (curl saves letters outside ASCII as _)"
}

// times formats a modification time for pages.
func times(t time.Time) (iso, text, title string) {
	t = t.UTC()
	return t.Format(time.RFC3339), t.Format("2 Jan 2006"), t.Format("2006-01-02 15:04 UTC")
}

func meta(folders, files int) string {
	var parts []string
	if folders > 0 {
		parts = append(parts, plural(folders, "folder", "folders"))
	}
	if files > 0 {
		parts = append(parts, plural(files, "file", "files"))
	}
	if parts == nil {
		return "Empty folder"
	}
	return strings.Join(parts, " · ")
}

func flash(uploaded, renamed int) string {
	s := plural(uploaded, "file", "files") + " uploaded."
	switch {
	case renamed == 1:
		s += " 1 was renamed because a file with the same name exists."
	case renamed > 1:
		s += fmt.Sprintf(" %s were renamed because files with the same names exist.", count(renamed))
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return count(n) + " " + many
}

// origin is the share's public origin as the visitor reached it.
func origin(r *http.Request) string {
	scheme := "https"
	if r.Header.Get("X-Forwarded-Proto") == "http" {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// problem is an answer that isn't the content asked for: its status, the copy
// of its page and where it leads. message doubles as the plain-text and JSON
// error body.
type problem struct {
	status  int
	code    string // the small code line: "404", "405 · uploads off"
	reason  string // for the title: "<reason> · Shared folder"
	heading string // fixed h1 text
	message string
	subject string // a path in message that pages set apart
	detail  string
	foot    string
	allow   string      // the Allow header of a 405
	back    *crumb      // the folder for the way back
	verb    string      // "Open" or "Back to"
	retry   bool        // offer "Try again"
	saved   []savedFile // files a failed multipart upload saved before it failed
	err     error       // what OnUpload reports for an upload that ends here
}

func errClosed() *problem {
	return &problem{status: http.StatusServiceUnavailable, code: "503", reason: "Closed",
		heading: "This share has stopped", message: "The owner stopped sharing these files."}
}

func errForbidden(msg string) *problem {
	return &problem{status: http.StatusForbidden, code: "403", reason: "Not allowed", heading: "Not allowed", message: msg}
}

func errUploadsOff() *problem {
	return &problem{status: http.StatusMethodNotAllowed, code: "405 · uploads off", reason: "Uploads are off",
		heading: "Uploads are off", message: "The person sharing this folder didn't allow uploads.",
		foot: "Sharing this yourself? Start it with tund serve --upload.", allow: "GET, HEAD"}
}

func errMethod(writes bool) *problem {
	allow := "GET, HEAD"
	if writes {
		allow = "GET, HEAD, PUT, POST"
	}
	return &problem{status: http.StatusMethodNotAllowed, code: "405", reason: "Not supported", heading: "Not supported",
		message: "This share can only list and download files (and upload them, when allowed).", allow: allow}
}

// errSecret refuses a file whose content looks like a secret. The copy avoids
// "private key": the edge's phishing heuristics count that phrase.
func errSecret() *problem {
	return &problem{status: http.StatusForbidden, code: "403", reason: "Not shared", heading: "Not shared",
		message: "This file isn't shared: it looks like it contains a secret, such as a key or an access token."}
}

// notFound is the one answer for everything missing or not shared. deepest is
// the last folder the path reached; the path a visitor sent is clipped.
func (s *Server) notFound(path string, deepest *node) *problem {
	path = clip(path)
	msg := "There is no file or folder at this address."
	if path != "" {
		msg = "There is no file or folder at " + path + ". It may have been renamed, moved or deleted."
	}
	p := &problem{status: http.StatusNotFound, code: "404", reason: "Not found", heading: "Nothing here", message: msg, subject: path, verb: "Open"}
	if deepest != nil && len(deepest.segs) > 0 {
		p.back = &crumb{Name: shown(deepest.name()), Href: deepest.href()}
	}
	return p
}

// readProblem answers an error from reading a resolved file or folder (what
// says which). Bodies never carry OS error text: it holds absolute paths.
func (s *Server) readProblem(err error, what string) *problem {
	switch classify(err) {
	case errGone:
		return s.notFound("", nil)
	case errDenied:
		return errForbidden("The owner's computer can't read this " + what + ".")
	case errBusy:
		return &problem{status: http.StatusLocked, code: "423", reason: "In use", heading: "In use",
			message: "This " + what + " is in use by another program on the owner's computer. Try again later."}
	}
	return &problem{status: http.StatusInternalServerError, code: "500", reason: "Error", heading: "Something went wrong",
		message: "The owner's computer couldn't read this " + what + ". Try again in a moment.", retry: true}
}

// fail answers with p as a page, JSON or one line of text.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, p *problem) {
	h := w.Header()
	if p.allow != "" {
		h.Set("Allow", p.allow)
	}
	h.Set("Cache-Control", "no-store")
	h.Add("Vary", "Accept")
	switch formatOf(r) {
	case formatHTML:
		e := errorPage{page: s.base(r, p.reason), Status: p.status, Code: p.code, Heading: p.heading,
			Message: p.message, Detail: p.detail, Foot: p.foot}
		if before, after, ok := strings.Cut(p.message, p.subject); ok && p.subject != "" {
			e.Before, e.Subject, e.After = before, p.subject, after
		}
		if len(p.saved) > 0 {
			var names []string
			for _, f := range p.saved {
				names = append(names, shown(f.Name))
			}
			e.Detail = strings.TrimSpace(e.Detail + " Saved before the error: " + strings.Join(names, ", ") + ".")
		}
		if p.retry {
			e.Actions = append(e.Actions, action{Label: "Try again", Href: s.here(r), Primary: true, Icon: "rotate-ccw"})
		}
		if p.back != nil {
			icon := "folder" // back to the folder an upload was for
			if p.verb == "Open" {
				icon = "folder-up" // the deepest folder of a path that went nowhere
			}
			e.Actions = append(e.Actions, action{Label: p.verb + " " + p.back.Name, Href: p.back.Href, Primary: len(e.Actions) == 0, Icon: icon})
		}
		// The share's root: its first folder, or the shared file's page.
		if p.back == nil || p.back.Href != "/" {
			e.Actions = append(e.Actions, action{Label: "Go to " + e.Share, Href: "/", Primary: len(e.Actions) == 0})
		}
		render(w, r, p.status, "error", e.Nonce, e)
	case formatJSON:
		v := map[string]any{"error": p.message}
		if p.saved != nil {
			v["files"] = p.saved
		}
		writeData(w, r, p.status, "application/json; charset=utf-8", jsonBytes(v))
	default:
		var b strings.Builder
		for _, f := range p.saved {
			b.WriteString(f.line() + "\n")
		}
		b.WriteString(p.message + "\n")
		writeData(w, r, p.status, textType, []byte(b.String()))
	}
}

// here is the request's own path in canonical form, for "Try again".
func (s *Server) here(r *http.Request) string {
	if segs, dir, ok := splitPath(r.URL.EscapedPath()); ok {
		return href(segs, dir)
	}
	return "/"
}
