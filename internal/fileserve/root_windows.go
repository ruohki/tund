package fileserve

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// realPath resolves p the way Windows does when it opens it: through
// symlinks, junctions, mount points and subst or mapped drives. Since Go
// 1.23 filepath.EvalSymlinks fails through junctions and mount points and
// leaves one at the end unresolved. p stays as typed when it can't be
// opened (os.Stat then tells why) or its volume has no drive letter.
func realPath(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(longPath(p))
	if err != nil {
		return p, nil
	}
	// No access rights: other programs' share modes and the ACL don't matter.
	h, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return p, nil
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	for {
		// Flags 0 are FILE_NAME_NORMALIZED (long names) and VOLUME_NAME_DOS
		// (drive letters).
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return p, nil
		}
		if n < uint32(len(buf)) {
			return dosPath(windows.UTF16ToString(buf[:n])), nil
		}
		buf = make([]uint16, n) // too small: n is the size needed
	}
}

// dosPath turns the \\?\ forms GetFinalPathNameByHandle returns into the
// usual ones: \\?\C:\x into C:\x, \\?\UNC\server\share into \\server\share.
func dosPath(p string) string {
	if rest, ok := strings.CutPrefix(p, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	return strings.TrimPrefix(p, `\\?\`)
}

// longPath adds the \\?\ prefix CreateFile needs for absolute paths of
// MAX_PATH and more (unless long paths are enabled system-wide). Like the
// os package it leaves paths shorter than 248 bytes alone.
func longPath(p string) string {
	switch {
	case len(p) < 248, strings.HasPrefix(p, `\\?\`), strings.HasPrefix(p, `\\.\`):
		return p
	case strings.HasPrefix(p, `\\`):
		return `\\?\UNC\` + p[2:]
	}
	return `\\?\` + p
}

// systemFolders are Windows' own folder, %SystemRoot%. The unix /dev would
// be <current drive>:\dev here, a common place for code.
func systemFolders() []string {
	if dir := os.Getenv("SystemRoot"); dir != "" {
		return []string{dir}
	}
	if dir, err := windows.GetSystemWindowsDirectory(); err == nil {
		return []string{dir}
	}
	return nil
}

// homeVolume returns where the volume that holds home is mounted when that
// isn't among home's parents: never here, where realPath names folders by
// their volume's drive letter.
func homeVolume(string) string { return "" }
