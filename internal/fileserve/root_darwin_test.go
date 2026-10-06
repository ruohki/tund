package fileserve

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// The Data volume holds /Users but isn't one of its parents: it and the
// folders above it (/System/Volumes, /System) contain the home folder.
func TestCheckRootDataVolume(t *testing.T) {
	home := homeDir()
	var st unix.Statfs_t
	if err := unix.Statfs(home, &st); err != nil {
		t.Fatal(err)
	}
	mnt := unix.ByteSliceToString(st.Mntonname[:])
	if mnt == "/" || strings.HasPrefix(home, mnt+"/") {
		t.Skipf("the volume holding %s is mounted at %s, a parent of it", home, mnt)
	}
	for p := mnt; p != "/"; p = filepath.Dir(p) {
		for _, upload := range []bool{false, true} {
			want := "refusing to share " + p + ": it contains your home folder"
			if _, err := CheckRoot(p, upload); err == nil || err.Error() != want {
				t.Errorf("CheckRoot(%s, upload %v) = %v\nwant %s", p, upload, err, want)
			}
		}
	}
}
