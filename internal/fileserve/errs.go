package fileserve

import (
	"errors"
	"io/fs"
)

// errKind is what an OS error means for visitors; errs_*.go map errno values.
type errKind int

const (
	errOther    errKind = iota
	errGone             // not there (any more)
	errDenied           // permission, macOS privacy settings, read-only volume
	errBusy             // locked by another program (Windows)
	errFull             // disk or quota full
	errLarge            // too large for the file system (FAT32: 4 GiB)
	errLongName         // name too long for the file system
)

func classifyGeneric(err error) errKind {
	switch {
	case errors.Is(err, errHidden), errors.Is(err, fs.ErrNotExist):
		return errGone
	case errors.Is(err, fs.ErrPermission):
		return errDenied
	}
	return errOther
}
