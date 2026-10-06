// Package fileserve is the file server behind tund serve. It lists and serves
// one shared file or folder and, when the owner allows it, saves uploads. It
// runs in-process behind the edge's password or single sign-on and answers
// only requests that passed it. Dotfiles and secret files are never listed or
// served, and uploads never replace a file.
package fileserve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures a Server.
type Options struct {
	Path   string // file or folder; CheckRoot rules apply
	Upload bool   // folders only: visitors may upload
	// OnUpload, if set, is called after every upload attempt that passed
	// admission (saved, refused, failed or canceled), from the request
	// goroutine. It must not block.
	OnUpload func(Upload)
}

// Upload describes one upload attempt for OnUpload.
type Upload struct {
	Path      string // share-relative, "/" separated, leading "/": where it was saved (or would have been)
	Requested string // the name the visitor sent (controls and bidi marks shown as \uXXXX)
	Size      int64  // bytes received
	Renamed   bool   // saved as "name (n).ext" because the name was taken
	Taken     string // when Renamed: the name that was taken (Requested after sanitizing)
	User      string // X-Tund-User-Email, -Name or -Username; "" for password visitors
	Remote    string // X-Forwarded-For
	Err       error  // nil = saved; otherwise one of the Err* values below or an OS error
}

// Upload failures reported to OnUpload. Their texts are short phrases for the
// owner's terminal; OS errors that fit none of them are passed through.
var (
	ErrCanceled     = errors.New("the upload broke off")
	ErrStopped      = errors.New("the share stopped")
	ErrDiskFull     = errors.New("the disk is full")
	ErrTooLarge     = errors.New("the file is too large for the disk")
	ErrReadOnly     = errors.New("the folder is read-only")
	ErrNameTaken    = errors.New("every variant of the name is taken")
	ErrBadName      = errors.New("the name can't be used")
	ErrRefused      = errors.New("names like this can't be uploaded")
	ErrNoFolder     = errors.New("no such folder")
	ErrInUse        = errors.New("in use by another program")
	ErrNotUpload    = errors.New("not a file upload")
	ErrTooManyParts = errors.New("too many files in one upload")
	ErrIsFolder     = errors.New("that's a folder")
	ErrResumed      = errors.New("resuming uploads isn't supported")
)

// Root is a share root that passed CheckRoot.
type Root struct {
	Path  string // absolute, symlinks resolved
	Shown string // Path for the owner's messages: $HOME as ~ on unix
	Dir   bool
	Name  string // base name
}

// Server is the http.Handler of a file share.
type Server struct {
	root     Root
	fsys     *os.Root // the shared folder, or the shared file's parent
	upload   bool
	onUpload func(Upload)
	cop      *http.CrossOriginProtection

	ctx    context.Context // canceled by Close
	cancel context.CancelFunc

	mu      sync.Mutex
	closed  bool
	uploads int             // uploads in progress
	drained chan struct{}   // closed when the last upload ends after Close
	temps   map[string]bool // share-relative temp files and reservations of ours on disk
	hidden  map[string]int  // names being reserved: hidden until their upload lands

	noLink    atomic.Bool // Root.Link failed once: commit by reserve and rename
	closeRoot sync.Once
	sniffs    sniffCache
}

// New checks opts.Path with CheckRoot and opens it for serving.
func New(opts Options) (*Server, error) {
	root, err := CheckRoot(opts.Path, opts.Upload)
	if err != nil {
		return nil, err
	}
	if opts.Upload && !root.Dir {
		return nil, fmt.Errorf("--upload needs a folder: visitors can only download a single shared file (%s)", root.Name)
	}
	dir := root.Path
	if !root.Dir {
		dir = filepath.Dir(root.Path)
	}
	fsys, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", opts.Path, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		root:     root,
		fsys:     fsys,
		upload:   opts.Upload,
		onUpload: opts.OnUpload,
		cop:      http.NewCrossOriginProtection(),
		ctx:      ctx,
		cancel:   cancel,
		temps:    map[string]bool{},
		hidden:   map[string]int{},
	}, nil
}

// Root returns the share root.
func (s *Server) Root() Root { return s.root }

// Close answers new requests with 503, cancels uploads in progress, waits
// until they removed their temp files or ctx ends, removes what is left and
// closes the share. Downloads in progress keep their open files.
func (s *Server) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	if s.uploads > 0 && s.drained == nil {
		s.drained = make(chan struct{})
	}
	drained := s.drained
	s.mu.Unlock()
	s.cancel()

	var err error
	if drained != nil {
		select {
		case <-drained:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	s.mu.Lock()
	left := make([]string, 0, len(s.temps))
	for rel := range s.temps {
		left = append(left, rel)
	}
	s.mu.Unlock()
	for _, rel := range left {
		s.remove(rel)
	}
	s.closeRoot.Do(func() { s.fsys.Close() })
	return err
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// beginUpload counts an upload in; false after Close.
func (s *Server) beginUpload() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.uploads++
	return true
}

func (s *Server) endUpload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads--
	if s.uploads == 0 && s.drained != nil {
		close(s.drained)
		s.drained = nil
	}
}

// track records a temp file or reservation of ours, so Close can remove it.
func (s *Server) track(rel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.temps[rel] = true
}

func (s *Server) untrack(rel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.temps, rel)
}

// hide keeps a name out of listings and downloads while an upload reserves
// it; every hide needs an unhide.
func (s *Server) hide(rel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hidden[rel]++
}

func (s *Server) unhide(rel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hidden[rel]--; s.hidden[rel] <= 0 {
		delete(s.hidden, rel)
	}
}

func (s *Server) isReserved(rel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hidden[rel] > 0
}

// remove deletes a temp file or reservation (retrying on Windows) and forgets it.
func (s *Server) remove(rel string) {
	if err := retry(func() error { return s.fsys.Remove(rel) }); err == nil || errors.Is(err, os.ErrNotExist) {
		s.untrack(rel)
	}
}

// ServeHTTP admits the request (4.1 of the spec: nothing touches the file
// system or the body before that) and dispatches it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A visitor who leaves ends the request's context, but a tunnel stream
	// keeps taking writes until its window fills and then blocks for minutes:
	// a past write deadline ends the response at once. Stopping it on return
	// keeps the deadline off the next request on the same stream.
	defer context.AfterFunc(r.Context(), func() {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
	})()
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	// Not no-referrer: then Safari 14-16 send Origin: null with the upload
	// form, which CrossOriginProtection refuses.
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	// On every response, files too: a page that opens the share can't tell
	// a file from a missing one by losing its hold on the window.
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	if s.isClosed() {
		s.fail(w, r, errClosed())
		return
	}
	if p := s.admit(r); p != nil {
		s.fail(w, r, p)
		return
	}
	segs, slash, ok := splitPath(r.URL.EscapedPath())
	if !ok {
		s.fail(w, r, s.notFound("", nil))
		return
	}
	switch {
	case !s.root.Dir:
		s.serveFileShare(w, r, segs, slash)
	case r.Method == http.MethodPut:
		s.put(w, r, segs, slash)
	case r.Method == http.MethodPost:
		s.post(w, r, segs)
	default:
		s.get(w, r, segs, slash)
	}
}

// admit applies the checks that need neither the file system nor the body.
func (s *Server) admit(r *http.Request) *problem {
	switch r.Header.Get("X-Tund-Auth") {
	case "password", "oidc":
	default:
		return errForbidden("This share only answers requests that passed its password or sign-in.")
	}
	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
		if len(xff) > 1 || net.ParseIP(strings.TrimSpace(xff[0])) == nil {
			return errForbidden("Replayed requests are not accepted by file shares.")
		}
	}
	writes := s.upload && s.root.Dir
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodPut, http.MethodPost:
		if !writes {
			return errUploadsOff()
		}
	default:
		return errMethod(writes)
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		dest := r.Header.Get("Sec-Fetch-Dest")
		navigate := (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			r.Header.Get("Sec-Fetch-Mode") == "navigate" && (dest == "document" || dest == "")
		if !navigate {
			return errForbidden("This request came from another website.")
		}
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		if err := s.cop.Check(r); err != nil {
			return errForbidden("This request came from another website.")
		}
	}
	return nil
}

// visitor returns who sent r: the signed-in user ("" for password visitors)
// and the X-Forwarded-For address.
func visitor(r *http.Request) (user, remote string) {
	if r.Header.Get("X-Tund-Auth") == "oidc" {
		for _, k := range []string{"X-Tund-User-Email", "X-Tund-User-Name", "X-Tund-User-Username"} {
			if v := strings.TrimSpace(r.Header.Get(k)); v != "" {
				user = shown(v)
				break
			}
		}
	}
	return user, shown(strings.TrimSpace(r.Header.Get("X-Forwarded-For")))
}

// report calls OnUpload. The names are printed in the owner's terminal: shown
// with visible escapes, and clipped, so a visitor can't flood it.
func (s *Server) report(r *http.Request, u Upload) {
	if s.onUpload == nil {
		return
	}
	u.Path, u.Requested, u.Taken = clipPath(shown(u.Path)), clip(shown(u.Requested)), clip(shown(u.Taken))
	u.User, u.Remote = visitor(r)
	s.onUpload(u)
}

// abs returns the absolute OS path of a share-relative path.
func (s *Server) abs(rel string) string {
	dir := s.root.Path
	if !s.root.Dir {
		dir = filepath.Dir(dir)
	}
	return filepath.Join(dir, filepath.FromSlash(rel))
}

// cancelReads lets Close interrupt a request body that is still arriving.
func (s *Server) cancelReads(w http.ResponseWriter) (stop func() bool) {
	return context.AfterFunc(s.ctx, func() {
		_ = http.NewResponseController(w).SetReadDeadline(time.Now())
	})
}
