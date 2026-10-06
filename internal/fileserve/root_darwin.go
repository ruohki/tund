package fileserve

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// realPath resolves symlinks.
func realPath(p string) (string, error) { return filepath.EvalSymlinks(p) }

// systemFolders are the kernel's folders.
func systemFolders() []string { return []string{"/proc", "/sys", "/dev"} }

// homeVolume returns where the volume that holds home is mounted. Since
// macOS 10.15 that is the Data volume at /System/Volumes/Data: /Users is a
// firmlink into it, so neither it nor its parents are parents of home.
func homeVolume(home string) string {
	var st unix.Statfs_t
	if unix.Statfs(home, &st) != nil {
		return ""
	}
	return unix.ByteSliceToString(st.Mntonname[:])
}
