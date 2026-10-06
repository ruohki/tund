package fileserve

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"tund/internal/secretfile"
)

// errHidden is every path that isn't shared. Missing, hidden and denied
// entries, special files and links that leave the share all answer the same
// 404, so visitors can't tell them apart.
var errHidden = errors.New("not shared")

// Resolution limits (variables for tests).
var (
	maxScan = 100_000 // entries read from one folder
	maxHops = 8       // symlinks followed while resolving one path
)

// node is a resolved share entry: the real file or folder behind a URL path.
type node struct {
	segs []string    // canonical URL segments: real names; a link keeps its own name
	rel  string      // real share-relative path, "/"-separated; "." is the share root
	info fs.FileInfo // Lstat of the real entry (never a link); nil for the share root
	dir  bool
}

func (n node) href() string { return href(n.segs, n.dir) }

// name is the entry's name in its folder ("" for the share root).
func (n node) name() string {
	if len(n.segs) == 0 {
		return ""
	}
	return n.segs[len(n.segs)-1]
}

// resolver resolves URL paths and symlink targets for one request. Only names
// that came out of a directory listing ever reach the os.Root, so the file
// system's case folding, Unicode normalization, 8.3 names and trailing-dot
// rules never apply to what a visitor typed.
type resolver struct {
	s    *Server
	hops int
	dirs map[string]*dirList
}

// dirList is a folder's names in directory order, read once per request.
type dirList struct {
	names  []string // at most maxScan
	capped bool     // the folder has more entries than that
}

func (s *Server) resolver() *resolver {
	return &resolver{s: s, dirs: map[string]*dirList{}}
}

// list reads the names in the real folder rel.
func (rv *resolver) list(rel string) (*dirList, error) {
	if d := rv.dirs[rel]; d != nil {
		return d, nil
	}
	f, err := rv.s.fsys.OpenFile(rel, os.O_RDONLY|oNonblock, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d := &dirList{}
	for {
		names, err := f.Readdirnames(min(1024, maxScan+1-len(d.names)))
		d.names = append(d.names, names...)
		if len(d.names) > maxScan {
			d.names, d.capped = d.names[:maxScan], true
			break
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	rv.dirs[rel] = d
	return d, nil
}

// lookup returns the real name in folder rel that a URL segment names: the
// entry with exactly that name, else the only entry whose NFC form equals the
// segment's (macOS keeps many names in NFD; typed URLs are NFC).
func (rv *resolver) lookup(rel, seg string) (string, error) {
	d, err := rv.list(rel)
	if err != nil {
		return "", err
	}
	if slices.Contains(d.names, seg) {
		return seg, nil
	}
	if !utf8.ValidString(seg) {
		return "", errHidden
	}
	want, found := norm.NFC.String(seg), ""
	for _, name := range d.names {
		if norm.NFC.String(name) == want {
			if found != "" {
				return "", errHidden
			}
			found = name
		}
	}
	if found == "" {
		return "", errHidden
	}
	return found, nil
}

// resolve walks segs from the share root. deepest is the last folder it
// reached, for the 404 page's way back.
func (rv *resolver) resolve(segs []string) (n, deepest node, err error) {
	n = node{rel: ".", dir: true}
	deepest = n
	for _, seg := range segs {
		if !n.dir {
			return node{}, deepest, errHidden
		}
		name, err := rv.lookup(n.rel, seg)
		if err != nil {
			return node{}, deepest, err
		}
		next, err := rv.entry(n.rel, name)
		if err != nil {
			return node{}, deepest, err
		}
		next.segs = append(slices.Clip(n.segs), name)
		if n = next; n.dir {
			deepest = n
		}
	}
	return n, deepest, nil
}

// entry resolves the entry name of the real folder dir to a visible folder or
// file, following a symlink inside the share. A link is shown under its own
// name, which must be visible too, and behaves like its target.
func (rv *resolver) entry(dir, name string) (node, error) {
	rel := join(dir, name)
	if rv.s.isReserved(rel) {
		return node{}, errHidden
	}
	info, err := rv.s.fsys.Lstat(rel)
	if err != nil {
		return node{}, err
	}
	switch t := typeOf(info); t {
	case typeLink:
		if osHidden(info) {
			return node{}, errHidden
		}
		target, err := rv.follow(dir, rel)
		if err != nil {
			return node{}, err
		}
		if !visibleName(name, target.dir) {
			return node{}, errHidden
		}
		return target, nil
	case typeDir, typeFile:
		if !visibleName(name, t == typeDir) || osHidden(info) {
			return node{}, errHidden
		}
		return node{rel: rel, info: info, dir: t == typeDir}, nil
	}
	return node{}, errHidden
}

// follow resolves the symlink rel in folder dir. Absolute targets are never
// followed; relative ones are spliced in, cleaned, must stay inside the share
// and are walked again from the root with the same rules as URLs, so a link
// can't reach a hidden or denied entry.
func (rv *resolver) follow(dir, rel string) (node, error) {
	if rv.hops++; rv.hops > maxHops {
		return node{}, errHidden
	}
	target, err := rv.s.fsys.Readlink(rel)
	if err != nil {
		return node{}, err
	}
	if target == "" || filepath.IsAbs(target) || filepath.VolumeName(target) != "" || os.IsPathSeparator(target[0]) {
		return node{}, errHidden
	}
	var parts []string
	if dir != "." {
		parts = strings.Split(dir, "/")
	}
	sep := func(r rune) bool { return r < utf8.RuneSelf && os.IsPathSeparator(byte(r)) } // "\" too on Windows
	for _, p := range strings.FieldsFunc(target, sep) {
		switch p {
		case ".":
		case "..":
			if len(parts) == 0 {
				return node{}, errHidden
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 { // the share root itself
		return node{}, errHidden
	}
	n, _, err := rv.resolve(parts)
	return n, err
}

// fileNode re-checks the shared file of a single-file share.
func (s *Server) fileNode() (node, error) {
	name := s.root.Name
	info, err := s.fsys.Lstat(name)
	if err != nil {
		return node{}, err
	}
	if typeOf(info) != typeFile || !visibleName(name, false) || osHidden(info) {
		return node{}, errHidden
	}
	return node{segs: []string{name}, rel: name, info: info}, nil
}

// open opens a resolved file for reading. Special files that replaced it
// can't block (O_NONBLOCK), and anything that isn't the resolved entry any
// more is refused.
func (s *Server) open(n node) (*os.File, fs.FileInfo, error) {
	f, err := s.fsys.OpenFile(n.rel, os.O_RDONLY|oNonblock, 0)
	if err != nil {
		return nil, nil, s.changed(n, err)
	}
	fi, err := f.Stat()
	if err == nil && (!openedFile(fi) || !os.SameFile(fi, n.info)) {
		err = errHidden
	}
	if err == nil { // the path must still be that file, not a link that led to it
		if now, lerr := s.fsys.Lstat(n.rel); lerr != nil || typeOf(now) != typeFile || !os.SameFile(now, fi) {
			err = errHidden
		}
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, fi, nil
}

// changed turns an error about a resolved entry into errHidden when the entry
// is gone or no longer the one that was resolved (swapped for a link that
// leaves the share, deleted, delete-pending on Windows): that is a missing
// path, not a server error. A file another program locked stays "in use".
func (s *Server) changed(n node, err error) error {
	if n.info == nil { // the share root: its handle can't be swapped
		return err
	}
	now, lerr := s.fsys.Lstat(n.rel)
	if lerr == nil && sameEntry(now, n.info) || lerr != nil && classify(lerr) == errBusy {
		return err
	}
	return errHidden
}

// sameEntry reports whether two Lstat results are the same file of the same
// type: some file systems hand a deleted file's inode number to the very next
// file, link or folder.
func sameEntry(a, b fs.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode().Type() == b.Mode().Type()
}

type fileType int

const (
	typeOther fileType = iota // FIFOs, sockets, devices, junctions
	typeFile
	typeDir
	typeLink
)

func typeOf(info fs.FileInfo) fileType {
	m := info.Mode()
	switch {
	case m&fs.ModeSymlink != 0:
		return typeLink
	case m.IsDir():
		return typeDir
	case m.IsRegular(), irregularFile(info):
		return typeFile
	}
	return typeOther
}

// junk are names systems create that visitors never need (Fold keys).
var junk = map[string]bool{
	"thumbs.db": true, "desktop.ini": true, "$recycle.bin": true,
	"system volume information": true, "lost+found": true,
}

// visibleName applies the name rules: no dotfiles, junk, control characters
// or backslashes, nothing the OS would read differently and no secret names.
func visibleName(name string, dir bool) bool {
	if name == "" || name[0] == '.' || strings.ContainsRune(name, '\\') ||
		strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) || !nameOK(name) {
		return false
	}
	if junk[secretfile.Fold(name)] {
		return false
	}
	_, denied := secretfile.Denied(name, dir)
	return !denied
}
