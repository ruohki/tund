package fileserve

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// markDownloaded tags an upload the way browsers tag downloads, so Gatekeeper
// treats it as coming from the internet. Best effort: FAT, exFAT and SMB
// volumes may not keep it.
func markDownloaded(f *os.File, _ string) {
	c, err := f.SyscallConn()
	if err != nil {
		return
	}
	v := fmt.Appendf(nil, "0081;%x;tund;", time.Now().Unix())
	_ = c.Control(func(fd uintptr) { _ = unix.Fsetxattr(int(fd), "com.apple.quarantine", v, 0) })
}
