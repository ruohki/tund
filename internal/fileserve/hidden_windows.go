package fileserve

import (
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// osHidden reports FILE_ATTRIBUTE_HIDDEN, which hides AppData and NTUSER.DAT
// without a leading dot.
func osHidden(info fs.FileInfo) bool { return attrs(info)&windows.FILE_ATTRIBUTE_HIDDEN != 0 }

// placeholder reports a cloud file (OneDrive and the like) whose content isn't
// on this disk: listings don't read it.
func placeholder(info fs.FileInfo) bool {
	return attrs(info)&(windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS|windows.FILE_ATTRIBUTE_OFFLINE) != 0
}

func attrs(info fs.FileInfo) uint32 {
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return d.FileAttributes
	}
	return 0
}

// irregularFile reports reparse points that are still plain files to serve:
// cloud placeholders, WOF-compressed and deduplicated files. Junctions and
// mount points carry the directory attribute and stay hidden.
func irregularFile(info fs.FileInfo) bool {
	m := info.Mode()
	return m&fs.ModeIrregular != 0 && m.Type()&^fs.ModeIrregular == 0 &&
		attrs(info)&windows.FILE_ATTRIBUTE_DIRECTORY == 0
}

// nameOK reports names Windows reads as written: no trailing dot or space,
// no device names and nothing else filepath.IsLocal refuses.
func nameOK(name string) bool {
	return !strings.HasSuffix(name, ".") && !strings.HasSuffix(name, " ") && filepath.IsLocal(name)
}
