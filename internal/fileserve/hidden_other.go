//go:build !darwin && !windows

package fileserve

import "io/fs"

// osHidden reports a hidden flag; here only dotfiles are hidden.
func osHidden(fs.FileInfo) bool { return false }

// placeholder reports files whose content isn't on this disk.
func placeholder(fs.FileInfo) bool { return false }

// irregularFile reports files the OS marks irregular that are still plain
// files to serve (Windows only).
func irregularFile(fs.FileInfo) bool { return false }

// nameOK reports names the OS reads as written (Windows drops trailing dots).
func nameOK(string) bool { return true }
