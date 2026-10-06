package fileserve

import (
	"errors"
	"io/fs"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// oNonblock is 0: Windows has no FIFOs that block an open.
const oNonblock = 0

// openedFile re-checks an opened file's type. Cloud and compressed files
// report ModeIrregular and are still files; pipes, devices and directories
// are not.
func openedFile(fi fs.FileInfo) bool {
	return fi.Mode()&(fs.ModeDir|fs.ModeSymlink|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeDevice) == 0
}

// classify sorts an OS error into what visitors are told.
func classify(err error) errKind {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return classifyGeneric(err)
	}
	switch errno {
	case windows.ERROR_FILE_NOT_FOUND, windows.ERROR_PATH_NOT_FOUND, windows.ERROR_INVALID_NAME, windows.ERROR_DIRECTORY,
		// Reparse points that open as nothing: WSL symlinks and FIFOs, app
		// execution aliases (ELOOP inside os.Root, 1920 outside it) and link
		// loops (1921), like ELOOP on unix.
		syscall.ELOOP, windows.ERROR_CANT_ACCESS_FILE, windows.ERROR_CANT_RESOLVE_FILENAME:
		return errGone
	case windows.ERROR_ACCESS_DENIED, windows.ERROR_WRITE_PROTECT:
		return errDenied
	case windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION:
		return errBusy
	case windows.ERROR_DISK_FULL, windows.ERROR_HANDLE_DISK_FULL:
		return errFull
	case windows.ERROR_FILE_TOO_LARGE:
		return errLarge
	case windows.ERROR_FILENAME_EXCED_RANGE:
		return errLongName
	}
	return classifyGeneric(err)
}

// retry runs fn again while antivirus scanners, indexers or sync clients hold
// the file open, for up to 2 s.
func retry(fn func() error) error {
	deadline := time.Now().Add(2 * time.Second)
	for delay := 10 * time.Millisecond; ; delay = min(2*delay, 200*time.Millisecond) {
		err := fn()
		if err == nil || !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
			time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
	}
}
