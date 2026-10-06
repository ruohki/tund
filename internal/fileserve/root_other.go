//go:build !darwin && !windows

package fileserve

import "path/filepath"

// realPath resolves symlinks.
func realPath(p string) (string, error) { return filepath.EvalSymlinks(p) }

// systemFolders are the kernel's folders.
func systemFolders() []string { return []string{"/proc", "/sys", "/dev"} }

// homeVolume returns where the volume that holds home is mounted when that
// isn't among home's parents; here it always is.
func homeVolume(string) string { return "" }
