package fileserve

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"tund/internal/secretfile"
)

// CheckRoot resolves a share root and applies the share-root policy: no file
// system roots, system folders, home folder or folders that contain it, no
// secret or dot folders anywhere in the path, no folders programs start
// from for --upload, and no secret single files. It reads the root once, so
// the macOS privacy prompt appears while the owner is at the terminal. Its
// errors are final CLI messages: they quote path as typed when it can't be
// used, and the resolved path (with $HOME as ~) when it is refused.
func CheckRoot(path string, upload bool) (Root, error) {
	typed, err := filepath.Abs(path)
	if err != nil {
		return Root{}, fmt.Errorf("cannot read %s: %v", path, err)
	}
	abs, err := realPath(typed)
	var info fs.FileInfo
	if err == nil {
		info, err = os.Stat(abs)
	}
	if err != nil {
		return Root{}, statError(path, err)
	}
	dir := info.IsDir()
	if !dir && typeOf(info) != typeFile {
		return Root{}, fmt.Errorf("%s is not a regular file or folder", path)
	}
	home := homeDir()
	shown := tilde(abs, home)
	if diskRoot(abs) {
		return Root{}, fmt.Errorf("refusing to share %s (the whole disk): pick a folder with just the files to share, e.g. tund serve %s --password …", abs, example(home, "Desktop", "to-share"))
	}
	if systemFolder(abs) {
		return Root{}, fmt.Errorf("refusing to share %s: it is a system folder", shown)
	}
	var homeInfo fs.FileInfo
	if home != "" {
		homeInfo, _ = os.Stat(home)
	}
	if homeInfo != nil {
		if os.SameFile(info, homeInfo) {
			return Root{}, fmt.Errorf("refusing to share your home folder (%s): it holds app data, browser profiles and keychains that can't be told apart from secrets. Share a folder inside it, e.g. tund serve %s --password …", tilde(home, home), example(home, "Documents", "to-share"))
		}
		for _, p := range homeHolders(home) {
			if pi, err := os.Stat(p); err == nil && os.SameFile(info, pi) {
				return Root{}, fmt.Errorf("refusing to share %s: it contains your home folder", shown)
			}
		}
	}
	comps := strings.FieldsFunc(abs[len(filepath.VolumeName(abs)):], func(r rune) bool { return r == '/' || r == filepath.Separator })
	for i, c := range comps {
		last := i == len(comps)-1
		if last && !dir {
			break // a single file's own name is checked below
		}
		rule, denied := secretfile.Denied(c, true)
		if !denied && !strings.HasPrefix(c, ".") {
			continue
		}
		switch {
		case !last:
			return Root{}, fmt.Errorf("refusing to share %s: it is inside %s, which is never shared", shown, c)
		case denied:
			return Root{}, fmt.Errorf("refusing to share %s: %s folders are never shared (%s)", shown, c, rule)
		}
		return Root{}, fmt.Errorf("refusing to share %s: dot folders are never shared", shown)
	}
	if upload && homeInfo != nil {
		if c := hiddenBelow(abs, homeInfo); c != "" {
			return Root{}, fmt.Errorf("refusing --upload for %s: programs start from folders inside %s. Share another folder for uploads", shown, c)
		}
	}
	name := filepath.Base(abs)
	if !dir {
		for _, n := range []string{name, filepath.Base(typed)} {
			if !visibleName(n, false) || osHidden(info) {
				return Root{}, fmt.Errorf("refusing to share %s: secret files like .env, keys and credentials are never shared", n)
			}
		}
	}
	if err := probe(abs, dir, info.Size(), name, filepath.Base(typed)); err != nil {
		var s secretError
		if errors.As(err, &s) {
			return Root{}, fmt.Errorf("refusing to share %s: it looks like it contains a secret (%s)", name, s.marker)
		}
		return Root{}, statError(path, err)
	}
	if name == "" || os.IsPathSeparator(name[0]) {
		name = cmp.Or(filepath.VolumeName(abs), "share")
	}
	return Root{Path: abs, Shown: shown, Dir: dir, Name: name}, nil
}

// statError explains why the root can't be used.
func statError(path string, err error) error {
	switch classify(err) {
	case errGone:
		return fmt.Errorf("%s does not exist", path)
	case errDenied:
		msg := "cannot read " + path + ": permission denied"
		if runtime.GOOS == "darwin" {
			msg += "; allow your terminal app in System Settings → Privacy & Security → Files and Folders"
		}
		return errors.New(msg)
	}
	return fmt.Errorf("cannot read %s: %v", path, err)
}

type secretError struct{ marker string }

func (e secretError) Error() string { return "contains a secret (" + e.marker + ")" }

// probe reads the root: the first names of a folder, or the start of a file,
// which it also sniffs for secrets.
func probe(abs string, dir bool, size int64, names ...string) error {
	f, err := os.OpenFile(abs, os.O_RDONLY|oNonblock, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if dir {
		if _, err := f.Readdirnames(1); err != nil && err != io.EOF {
			return err
		}
		return nil
	}
	if _, err := f.Read(make([]byte, 1)); err != nil && err != io.EOF {
		return err
	}
	kind, which := sniffKind(size, names...)
	if kind == secretfile.SniffNone {
		return nil
	}
	marker, found, err := secretfile.Sniff(f, size, which, kind)
	if err != nil {
		return err
	}
	if found {
		return secretError{marker}
	}
	return nil
}

// homeDir is the user's home folder resolved like share roots, or "".
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if h, err := realPath(home); err == nil {
		return h
	}
	return home
}

// homeHolders are the folders that contain home: its parents, also as $HOME
// spells them when home is a link, and the mount point of its volume with
// the mount point's parents (macOS keeps /Users on the Data volume, mounted
// at /System/Volumes/Data).
func homeHolders(home string) []string {
	var ps []string
	parents := func(p string) {
		for ; ; p = filepath.Dir(p) {
			ps = append(ps, p)
			if filepath.Dir(p) == p {
				return
			}
		}
	}
	parents(filepath.Dir(home))
	if raw, err := os.UserHomeDir(); err == nil && filepath.IsAbs(raw) && filepath.Clean(raw) != home {
		parents(filepath.Dir(filepath.Clean(raw)))
	}
	if mnt := homeVolume(home); mnt != "" {
		parents(mnt)
	}
	return ps
}

// tilde abbreviates the home folder as ~ on unix.
func tilde(p, home string) string {
	if runtime.GOOS == "windows" || home == "" || home == "/" {
		return p
	}
	if p == home || strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// example is a folder under home to suggest in a message: ~/a/b where shells
// expand ~, the full path on Windows, where cmd.exe doesn't (quoted when it
// has spaces; double quotes work in cmd.exe and PowerShell alike).
func example(home string, elem ...string) string {
	if runtime.GOOS != "windows" || home == "" {
		return "~/" + strings.Join(elem, "/")
	}
	p := filepath.Join(append([]string{home}, elem...)...)
	if strings.ContainsRune(p, ' ') {
		p = `"` + p + `"`
	}
	return p
}

// diskRoot reports / or, on Windows, the system drive's root.
func diskRoot(abs string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(abs), cmp.Or(os.Getenv("SystemDrive"), "C:")+`\`)
	}
	return filepath.Dir(abs) == abs
}

// systemFolder reports paths inside the OS's own folders (systemFolders),
// compared as files so case-insensitive spellings can't slip by.
func systemFolder(abs string) bool {
	var sys []fs.FileInfo
	for _, p := range systemFolders() {
		if fi, err := os.Stat(p); err == nil {
			sys = append(sys, fi)
		}
	}
	for p := abs; ; p = filepath.Dir(p) {
		if fi, err := os.Stat(p); err == nil {
			for _, s := range sys {
				if os.SameFile(fi, s) {
					return true
				}
			}
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}

// hiddenBelow returns the first folder between home and abs (or abs itself)
// that the OS hides, like ~/Library on macOS or AppData on Windows: programs
// start from folders in there. "" if there is none or abs isn't in home.
func hiddenBelow(abs string, home fs.FileInfo) string {
	var below []string // abs and its parents up to home, innermost first
	for p := abs; ; p = filepath.Dir(p) {
		if fi, err := os.Stat(p); err == nil && os.SameFile(fi, home) {
			break
		}
		below = append(below, p)
		if filepath.Dir(p) == p {
			return "" // not inside home
		}
	}
	for i := len(below) - 1; i >= 0; i-- {
		if fi, err := os.Lstat(below[i]); err == nil && osHidden(fi) {
			return filepath.Base(below[i])
		}
	}
	return ""
}
