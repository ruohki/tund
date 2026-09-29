//go:build !windows

package client

import "os"

func enableVT(*os.File) bool { return true }
