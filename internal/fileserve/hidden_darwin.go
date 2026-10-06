package fileserve

import (
	"io/fs"
	"syscall"

	"golang.org/x/sys/unix"
)

// osHidden reports Finder's hidden flag (UF_HIDDEN), which hides ~/Library
// without a leading dot.
func osHidden(info fs.FileInfo) bool { return flags(info)&unix.UF_HIDDEN != 0 }

// placeholder reports an iCloud "Optimize Storage" file whose content is
// still in the cloud (SF_DATALESS): listings don't read it.
func placeholder(info fs.FileInfo) bool { return flags(info)&unix.SF_DATALESS != 0 }

func flags(info fs.FileInfo) uint32 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Flags
	}
	return 0
}

// irregularFile reports files the OS marks irregular that are still plain
// files to serve (Windows only).
func irregularFile(fs.FileInfo) bool { return false }

// nameOK reports names the OS reads as written (Windows drops trailing dots).
func nameOK(string) bool { return true }
