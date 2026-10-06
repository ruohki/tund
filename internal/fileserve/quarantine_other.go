//go:build !darwin && !windows

package fileserve

import "os"

// markDownloaded would tag an upload as coming from the internet; there is no
// such mark here.
func markDownloaded(*os.File, string) {}
