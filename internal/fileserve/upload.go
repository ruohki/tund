package fileserve

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"tund/internal/secretfile"
)

// Upload limits and OS hooks (variables for tests).
var (
	maxParts   = 1000            // multipart parts per request
	floorEvery = int64(16 << 20) // re-check free space after this many bytes
	diskSpace  = volumeSpace     // free and total bytes of the volume holding a folder
	linkFile   = (*os.Root).Link // commits an upload without ever replacing a file
)

// Names visitors send: the longest one sanitize looks at, and how much of one
// responses and the owner's terminal repeat.
const (
	maxRawName = 4 << 10
	maxShown   = 255
)

// tempPrefix starts the names of uploads in progress; the leading dot keeps
// them out of listings.
const tempPrefix = ".tund-upload-"

// savedFile is one stored upload, as upload responses describe it.
type savedFile struct {
	Name      string `json:"name"`      // the final name
	Requested string `json:"requested"` // the name the visitor sent
	Size      int64  `json:"size"`
	Renamed   bool   `json:"renamed"`
	URL       string `json:"url"` // absolute, escaped
	path      string // the URL path for messages: "/inbox/report (1).pdf"
	taken     string // the sanitized name, when it was taken
}

// line is the text response for one saved file:
// "saved /inbox/report (1).pdf (2.4 MB; report.pdf already existed)".
func (f savedFile) line() string {
	s := "saved " + f.path + " (" + humanBytes(f.Size)
	if f.Renamed {
		s += "; " + shown(f.taken) + " already existed"
	}
	return s + ")"
}

// put answers PUT /dir/name: the body is the file "name" for folder /dir
// (curl -T file https://host/dir/ sends exactly that).
func (s *Server) put(w http.ResponseWriter, r *http.Request, segs []string, slash bool) {
	if !s.beginUpload() {
		s.fail(w, r, errClosed())
		return
	}
	defer s.endUpload()
	defer s.cancelReads(w)()
	folder, requested := segs, ""
	if !slash && len(segs) > 0 {
		folder, requested = segs[:len(segs)-1], segs[len(segs)-1]
	}
	rv := s.resolver()
	dir, deepest, err := rv.resolve(folder)
	if err != nil || !dir.dir {
		s.report(r, Upload{Path: pathText(segs, slash), Requested: requested, Err: ErrNoFolder})
		s.fail(w, r, s.notFound(pathText(folder, true), &deepest))
		return
	}
	// refuse answers before the body is read, so Expect: 100-continue
	// clients never send it; name is the file the upload was for.
	refuse := func(p *problem, name string) {
		s.report(r, Upload{Path: uploadPath(dir, name), Requested: requested, Err: p.err})
		s.back(p, dir)
		s.fail(w, r, p)
	}
	if requested == "" { // curl -T - https://host/dir/
		refuse(&problem{status: http.StatusBadRequest, code: "400", reason: "No file name", heading: "Add a file name",
			message: "Add a file name: curl -T file " + origin(r) + dir.href(), err: ErrBadName}, "")
		return
	}
	rv.hops = 0
	// curl -T file https://host/inbox means the folder, so say so instead of
	// saving "inbox (1)" beside it. The page's uploader (JSON) always names
	// the file, so a file called like a folder just gets a number.
	if sub, _, err := rv.resolve(segs); err == nil && sub.dir && formatOf(r) != formatJSON {
		msg := shown(sub.name()) + " is a folder: end the URL with a slash to upload into it: curl -T file " + origin(r) + sub.href()
		refuse(&problem{status: http.StatusConflict, code: "409", reason: "Folder address", heading: "That address is a folder",
			message: msg, err: ErrIsFolder}, sub.name())
		return
	}
	if resumed(r) {
		refuse(errBadRequest("This share can't resume uploads: send the whole file again (without -C).", ErrResumed), baseName(requested))
		return
	}
	name, p := sanitize(requested)
	if p == nil && !s.spaceFor(dir.rel, r.ContentLength) {
		p = errDiskFull(name)
	}
	if p != nil {
		refuse(p, cmp.Or(name, baseName(requested)))
		return
	}
	f, p := s.save(r, dir, requested, name, r.Body)
	if p != nil {
		s.back(p, dir)
		s.fail(w, r, p)
		return
	}
	s.uploaded(w, r, dir, []savedFile{f}, false)
}

// post answers a multipart POST to a folder: every part with a file name is
// a file, whatever its field name. Parts are read as they arrive (never
// ParseMultipartForm, which spools to the temp folder) and each file is
// committed once its part ended.
func (s *Server) post(w http.ResponseWriter, r *http.Request, segs []string) {
	if !s.beginUpload() {
		s.fail(w, r, errClosed())
		return
	}
	defer s.endUpload()
	defer s.cancelReads(w)()
	dir, deepest, err := s.resolver().resolve(segs)
	if err != nil || !dir.dir {
		s.report(r, Upload{Path: pathText(segs, true), Err: ErrNoFolder})
		s.fail(w, r, s.notFound(pathText(segs, true), &deepest))
		return
	}
	var saved []savedFile
	failed := func(p *problem) {
		p.saved = saved
		s.back(p, dir)
		s.fail(w, r, p)
	}
	// refuse reports an upload that ends before its next file is saved;
	// name is that file, if known.
	refuse := func(p *problem, requested, name string) {
		s.report(r, Upload{Path: uploadPath(dir, name), Requested: requested, Err: p.err})
		failed(p)
	}
	body := &bodyRead{r: r.Body}
	r.Body = struct {
		io.Reader
		io.Closer
	}{body, r.Body}
	mr, err := r.MultipartReader()
	if err != nil {
		refuse(errBadRequest("Send files as multipart/form-data, or PUT them one at a time.", ErrNotUpload), "", "")
		return
	}
	if !s.spaceFor(dir.rel, r.ContentLength) {
		refuse(errDiskFull(""), "", "")
		return
	}
	for parts := 1; ; parts++ {
		part, err := mr.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			p := errBadRequest("This upload isn't well-formed multipart/form-data.", ErrNotUpload)
			if body.err != nil { // the body broke off, not its format
				p = uploadProblem(s.bodyError(), "")
			}
			refuse(p, "", "")
			return
		}
		if parts > maxParts {
			refuse(errBadRequest(fmt.Sprintf("An upload can have at most %s parts.", count(maxParts)), ErrTooManyParts), "", "")
			return
		}
		requested, ok := partFileName(part)
		if !ok {
			continue
		}
		// The floor before every file: while a part streams, it is checked
		// only every floorEvery bytes.
		name, p := sanitize(requested)
		if p == nil && !s.spaceFor(dir.rel, 0) {
			p = errDiskFull(name)
		}
		if p != nil {
			refuse(p, requested, cmp.Or(name, baseName(requested)))
			return
		}
		f, p := s.save(r, dir, requested, name, part)
		if p != nil {
			failed(p)
			return
		}
		saved = append(saved, f)
	}
	if len(saved) == 0 {
		refuse(errBadRequest("The upload had no files.", ErrNotUpload), "", "")
		return
	}
	s.uploaded(w, r, dir, saved, true)
}

// bodyRead remembers whether reading a request body failed: a visitor or
// Close cut the upload off, rather than it being malformed.
type bodyRead struct {
	r   io.Reader
	err error
}

func (b *bodyRead) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil && err != io.EOF {
		b.err = err
	}
	return n, err
}

// partFileName returns the filename parameter of a part. Part.FileName
// would apply the host's filepath.Base, which differs between OSes; sanitize
// does that job the same way everywhere. Forms escape only ", CR and LF in
// it (as %22, %0D and %0A): undone, so a form saves the name a PUT would.
func partFileName(p *multipart.Part) (string, bool) {
	_, params, err := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
	if err != nil {
		return "", false
	}
	name := formEscapes.Replace(params["filename"])
	return name, name != ""
}

var formEscapes = strings.NewReplacer("%22", `"`, "%0D", "\r", "%0A", "\n")

// resumed reports a PUT that sends only part of a file (curl -C): any
// Content-Range but the whole body's, "bytes 0-(n-1)/n" for n bytes.
func resumed(r *http.Request) bool {
	ranges := r.Header.Values("Content-Range")
	if len(ranges) == 0 {
		return false
	}
	n := r.ContentLength
	return len(ranges) > 1 || n <= 0 || !strings.EqualFold(strings.TrimSpace(ranges[0]), fmt.Sprintf("bytes 0-%d/%d", n-1, n))
}

// back points an upload error page back at the folder.
func (s *Server) back(p *problem, dir node) {
	if p.back == nil && p.status != http.StatusNotFound {
		name := shown(dir.name())
		if len(dir.segs) == 0 {
			name = shown(s.root.Name)
		}
		p.back, p.verb = &crumb{Name: name, Href: dir.href()}, "Back to"
	}
}

// uploaded answers a successful upload: 201 with the file's URL as JSON or
// text, or for a browser's form a 303 back to the folder with a summary.
func (s *Server) uploaded(w http.ResponseWriter, r *http.Request, dir node, files []savedFile, multi bool) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Add("Vary", "Accept")
	renamed := 0
	for _, f := range files {
		if f.Renamed {
			renamed++
		}
	}
	switch formatOf(r) {
	case formatHTML:
		if multi {
			h.Set("Location", fmt.Sprintf("%s?uploaded=%d&renamed=%d", dir.href(), len(files), renamed))
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		fallthrough
	case formatText:
		h.Set("Location", files[0].URL)
		var b strings.Builder
		for _, f := range files {
			b.WriteString(f.line() + "\n")
		}
		writeData(w, r, http.StatusCreated, textType, []byte(b.String()))
	case formatJSON:
		h.Set("Location", files[0].URL)
		var v any = files[0]
		if multi {
			v = map[string]any{"files": files}
		}
		writeData(w, r, http.StatusCreated, "application/json; charset=utf-8", jsonBytes(v))
	}
}

// save streams body into folder dir as name, or the first free "name (n)",
// and reports the attempt.
func (s *Server) save(r *http.Request, dir node, requested, name string, body io.Reader) (savedFile, *problem) {
	final, n, err := s.receive(r.Context(), dir, name, body)
	u := Upload{Path: uploadPath(dir, name), Requested: requested, Size: n}
	if err != nil {
		p := uploadProblem(err, name)
		u.Err = p.err
		s.report(r, u)
		return savedFile{}, p
	}
	u.Path, u.Renamed = uploadPath(dir, final), final != name
	if u.Renamed {
		u.Taken = name
	}
	s.report(r, u)
	segs := append(dir.segs[:len(dir.segs):len(dir.segs)], final)
	return savedFile{Name: final, Requested: clip(requested), Size: n, Renamed: u.Renamed, URL: href(segs, false),
		path: pathText(segs, false), taken: name}, nil
}

// baseName is what follows the last "/" or "\" of a name a visitor sent.
func baseName(raw string) string {
	return raw[strings.LastIndexAny(raw, `/\`)+1:]
}

// clip cuts s to maxShown bytes on a rune boundary and marks the cut with "…".
func clip(s string) string {
	if len(s) <= maxShown {
		return s
	}
	n := maxShown
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// clipPath clips a share path's folders and its last name apart, so a long
// path keeps its file name.
func clipPath(p string) string {
	i := strings.LastIndexByte(p, '/')
	if len(p) <= maxShown || i < 0 {
		return clip(p)
	}
	return clip(p[:i]) + "/" + clip(p[i+1:])
}

// uploadPath is where an upload lands, for OnUpload: the real share-relative
// path with a leading "/".
func uploadPath(dir node, name string) string {
	if dir.rel == "." {
		return "/" + name
	}
	return "/" + join(dir.rel, name)
}

// receive writes body to a temp file in folder dir and commits it. The temp
// file is synced and marked as downloaded from the internet before it gets
// its final name, and removed on every failure. ctx is the request's: an
// upload whose visitor left while it was being saved isn't committed.
func (s *Server) receive(ctx context.Context, dir node, name string, body io.Reader) (final string, n int64, err error) {
	tmp := join(dir.rel, tempPrefix+randomHex())
	f, err := s.fsys.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return "", 0, err
	}
	s.track(tmp)
	n, err = s.copyBody(f, body, dir.rel)
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		markDownloaded(f, s.abs(tmp))
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && s.ctx.Err() != nil {
		err = ErrStopped
	}
	if err == nil && ctx.Err() != nil {
		err = ErrCanceled
	}
	if err == nil {
		final, err = s.commit(dir.rel, tmp, name)
	}
	if err != nil {
		s.remove(tmp)
	}
	return final, n, err
}

// copyBody copies an upload, re-checking the free-space floor as it grows.
func (s *Server) copyBody(f *os.File, body io.Reader, dir string) (int64, error) {
	buf := make([]byte, 256<<10)
	var n int64
	next := floorEvery
	for {
		nr, rerr := body.Read(buf)
		if nr > 0 {
			nw, werr := f.Write(buf[:nr])
			n += int64(nw)
			if werr != nil {
				return n, werr
			}
			if n >= next {
				next = n + floorEvery
				if !s.spaceFor(dir, 0) {
					return n, ErrDiskFull
				}
			}
		}
		if rerr == io.EOF {
			return n, nil
		}
		if rerr != nil {
			return n, s.bodyError()
		}
	}
}

// bodyError is why a request body broke off: Close, or the visitor.
func (s *Server) bodyError() error {
	if s.ctx.Err() != nil {
		return ErrStopped
	}
	return ErrCanceled
}

// spaceFor reports whether the volume of folder dir keeps its free-space
// floor (1 GiB or 5% of the volume, whichever is less) after want more bytes
// (want < 0: unknown). Volumes whose space can't be read always pass, and so
// do those that report no size (FUSE without statfs, ramfs).
func (s *Server) spaceFor(dir string, want int64) bool {
	free, total, err := diskSpace(s.abs(dir))
	if err != nil || total == 0 {
		return true
	}
	floor := min(uint64(1<<30), total/20)
	return free >= floor && free-floor >= uint64(max(want, 0))
}

// commit gives the temp file its final name without ever replacing a file:
// the file system decides what "the same name" means (APFS and NTFS fold
// case and Unicode), because Link fails on any existing name. Where hard
// links don't work (FAT, exFAT, some network shares) a name is reserved with
// an exclusive create and the temp file renamed over that empty reservation.
func (s *Server) commit(dir, tmp, name string) (string, error) {
	stem, ext := splitExt(name)
	for i := range 1000 {
		cand := name
		if i > 0 {
			cand = fit(stem, fmt.Sprintf(" (%d)", i), ext)
		}
		if _, refused := secretfile.UploadRefused(cand); refused {
			continue
		}
		rel := join(dir, cand)
		if !s.noLink.Load() {
			err := linkFile(s.fsys, tmp, rel)
			if err == nil {
				s.remove(tmp)
				return cand, nil
			}
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			s.noLink.Store(true)
		}
		ok, err := s.reserve(tmp, rel)
		if err != nil {
			return "", err
		}
		if ok {
			return cand, nil
		}
	}
	return "", ErrNameTaken
}

// reserve claims rel with an exclusive create and renames tmp over it; false
// if the name is taken. The reservation stays hidden until the rename is done.
func (s *Server) reserve(tmp, rel string) (bool, error) {
	s.hide(rel)
	defer s.unhide(rel)
	f, err := s.fsys.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.track(rel)
	f.Close()
	if err := retry(func() error { return s.fsys.Rename(tmp, rel) }); err != nil {
		s.remove(rel)
		return false, err
	}
	s.untrack(rel)
	s.untrack(tmp)
	return true, nil
}

func randomHex() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// sanitize turns the name a visitor sent into the name to save, the same way
// on every OS, before any other check (names over maxRawName bytes → 400):
//  1. keep what follows the last "/" or "\" (never filepath.Base);
//  2. invalid UTF-8 becomes "_"; 3. NFC;
//  4. drop control characters, bidi marks and invisible formatting (keeping
//     ZWJ and ZWNJ); 5. < > : " | ? * become "_";
//  6. trim leading white space, and trailing white space and dots;
//  7. empty → 400, a leading dot → 403 (hidden names can't be uploaded);
//  8. a leading "-" gets a "_" prefix (option injection);
//  9. Windows device names (CON, COM1, LPT¹, CONIN$ ...) get a "_" prefix;
//  10. at most 255 bytes, cutting the stem and keeping the extension;
//  11. names that are secret or that programs pick up on their own → 403.
func sanitize(raw string) (string, *problem) {
	if len(raw) > maxRawName {
		p := errBadName()
		p.message = "That file name is too long."
		return "", p
	}
	s := norm.NFC.String(strings.ToValidUTF8(baseName(raw), "_"))
	s = strings.Map(func(r rune) rune {
		switch {
		case invisible(r), r == 0x200b, r >= 0x2060 && r <= 0x2064, r == 0xfeff:
			return -1
		case strings.ContainsRune(`<>:"|?*`, r):
			return '_'
		}
		return r
	}, s)
	s = norm.NFC.String(s) // dropping marks can leave combining characters to compose
	s = trimName(s)
	switch {
	case s == "":
		return "", errBadName()
	case s[0] == '.':
		return "", &problem{status: http.StatusForbidden, code: "403", reason: "Not allowed", heading: "Not allowed",
			message: "Files whose names start with a dot can't be uploaded to this share.", err: ErrRefused}
	}
	if s[0] == '-' {
		s = "_" + s
	}
	if deviceName(s) {
		s = "_" + s
	}
	if len(s) > 255 {
		stem, ext := splitExt(s)
		s = trimName(fit(stem, "", ext))
	}
	if _, refused := secretfile.UploadRefused(s); refused || !visibleName(s, false) {
		return "", &problem{status: http.StatusForbidden, code: "403", reason: "Not allowed", heading: "Not allowed",
			message: "Files named like this can't be uploaded to this share.", err: ErrRefused}
	}
	return s, nil
}

// trimName trims leading white space, and trailing white space and dots
// (Windows drops those silently).
func trimName(s string) string {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	return strings.TrimRightFunc(s, func(r rune) bool { return r == '.' || unicode.IsSpace(r) })
}

// deviceName reports names Windows treats as devices: the part before the
// first dot, without trailing spaces, is CON, PRN, AUX, NUL, COM0-9, LPT0-9,
// COM¹²³, LPT¹²³, CONIN$ or CONOUT$ in any case.
func deviceName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	for _, p := range []string{"COM", "LPT"} {
		if n, ok := strings.CutPrefix(stem, p); ok && (len(n) == 1 && '0' <= n[0] && n[0] <= '9' || n == "¹" || n == "²" || n == "³") {
			return true
		}
	}
	return false
}

// splitExt splits a name into stem and extension: ".tar.gz"-style double
// extensions stay together; otherwise the last dot counts if it isn't the
// first character and the extension is at most 16 bytes without spaces.
func splitExt(name string) (stem, ext string) {
	lower := strings.ToLower(name)
	for _, c := range []string{"gz", "bz2", "xz", "zst", "lz", "lz4", "lzma", "br", "z"} {
		if x := ".tar." + c; len(name) > len(x) && strings.HasSuffix(lower, x) {
			return name[:len(name)-len(x)], name[len(name)-len(x):]
		}
	}
	i := strings.LastIndexByte(name, '.')
	if i <= 0 || len(name)-i > 17 || strings.ContainsFunc(name[i:], unicode.IsSpace) {
		return name, ""
	}
	return name[:i], name[i:]
}

// fit joins stem, suffix and ext, cutting the stem on a rune boundary so the
// result has at most 255 bytes.
func fit(stem, suffix, ext string) string {
	if n := 255 - len(suffix) - len(ext); len(stem) > n {
		for n > 0 && !utf8.RuneStart(stem[n]) {
			n--
		}
		stem = stem[:max(n, 0)]
	}
	return stem + suffix + ext
}

// Upload problems. err is what OnUpload reports.
func errBadName() *problem {
	return &problem{status: http.StatusBadRequest, code: "400", reason: "Bad name", heading: "That file name can't be used",
		message: "Names can't be empty, start with a dot or be longer than 255 bytes.", err: ErrBadName}
}

func errBadRequest(msg string, err error) *problem {
	return &problem{status: http.StatusBadRequest, code: "400", reason: "Bad request", heading: "That upload didn't work",
		message: msg, err: err}
}

func errDiskFull(name string) *problem {
	return &problem{status: http.StatusInsufficientStorage, code: "507", reason: "Not enough space", heading: "Not enough space",
		message: "The owner's disk is full, so " + what(name) + " wasn't saved.", err: ErrDiskFull}
}

// what names an upload in messages: the file, or "the upload" before a name
// is known.
func what(name string) string {
	if name == "" {
		return "the upload"
	}
	return shown(name)
}

// uploadProblem answers a failed save. Bodies never carry OS error text.
func uploadProblem(err error, name string) *problem {
	switch {
	case errors.Is(err, ErrStopped):
		p := errClosed()
		p.err = ErrStopped
		return p
	case errors.Is(err, ErrCanceled):
		msg := "The upload broke off before it was complete."
		if name != "" {
			msg = "The upload broke off before " + shown(name) + " was complete."
		}
		return &problem{status: http.StatusBadRequest, code: "400", reason: "Canceled", heading: "The upload stopped",
			message: msg, err: ErrCanceled}
	case errors.Is(err, ErrDiskFull):
		return errDiskFull(name)
	case errors.Is(err, ErrNameTaken):
		return &problem{status: http.StatusConflict, code: "409", reason: "Name taken", heading: "Too many files with this name",
			message: "This folder already has " + what(name) + " and 999 numbered copies of it.", err: ErrNameTaken}
	}
	switch classify(err) {
	case errGone: // the folder went away while the file arrived
		return &problem{status: http.StatusNotFound, code: "404", reason: "Not found", heading: "Nothing here",
			message: "The folder was removed before " + what(name) + " was saved.", err: ErrNoFolder}
	case errFull:
		return errDiskFull(name)
	case errLarge:
		return &problem{status: http.StatusRequestEntityTooLarge, code: "413", reason: "Too large", heading: "Too large",
			message: "The owner's disk can't store files this large.", err: ErrTooLarge}
	case errDenied:
		return &problem{status: http.StatusForbidden, code: "403", reason: "Read-only", heading: "Read-only",
			message: "This folder is read-only on the owner's computer.", err: ErrReadOnly}
	case errLongName:
		p := errBadName()
		p.message = "That file name is too long for the owner's computer."
		return p
	case errBusy:
		return &problem{status: http.StatusLocked, code: "423", reason: "In use", heading: "In use",
			message: "The folder is in use by another program on the owner's computer. Try again later.", err: ErrInUse}
	}
	return &problem{status: http.StatusInternalServerError, code: "500", reason: "Error", heading: "Something went wrong",
		message: "The owner's computer couldn't save " + what(name) + ". Try again in a moment.", err: err}
}
