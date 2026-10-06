//go:build unix

package fileserve

import (
	"errors"
	"io/fs"
	"syscall"
)

// oNonblock keeps opening a FIFO from blocking until a writer appears.
const oNonblock = syscall.O_NONBLOCK

// openedFile re-checks an opened file's type: only regular files are served.
func openedFile(fi fs.FileInfo) bool { return fi.Mode().IsRegular() }

// classify sorts an OS error into what visitors are told.
func classify(err error) errKind {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return classifyGeneric(err)
	}
	switch errno {
	case syscall.ENOENT, syscall.ENOTDIR, syscall.ELOOP:
		return errGone
	case syscall.EACCES, syscall.EPERM, syscall.EROFS:
		return errDenied
	case syscall.ENOSPC, syscall.EDQUOT:
		return errFull
	case syscall.EFBIG:
		return errLarge
	case syscall.ENAMETOOLONG:
		return errLongName
	}
	return errOther
}

// retry runs fn once; only Windows has to wait for other programs' handles.
func retry(fn func() error) error { return fn() }
