//go:build !unix && !windows

package fileserve

import "io/fs"

const oNonblock = 0

// openedFile re-checks an opened file's type: only regular files are served.
func openedFile(fi fs.FileInfo) bool { return fi.Mode().IsRegular() }

// classify sorts an OS error into what visitors are told.
func classify(err error) errKind { return classifyGeneric(err) }

// retry runs fn once.
func retry(fn func() error) error { return fn() }
