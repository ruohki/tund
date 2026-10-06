package fileserve

import "os"

// markDownloaded adds the Mark of the Web, so SmartScreen and Office's
// Protected View treat an upload as coming from the internet. Best effort:
// FAT and exFAT have no alternate data streams. The stream moves with the
// temp file when it is linked or renamed into place.
func markDownloaded(_ *os.File, path string) {
	_ = os.WriteFile(path+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\n"), 0o644)
}
