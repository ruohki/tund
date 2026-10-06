package fileserve

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// junction makes a directory junction, which needs no privileges.
func junction(t *testing.T, target, link string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v: %s", err, out)
	}
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// filepath.EvalSymlinks fails through junctions ("does not exist") and keeps
// a junction root as typed, so .ssh behind one looked like any folder.
func TestCheckRootJunctions(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	mkdirs(t, filepath.Join(home, ".ssh"), filepath.Join(home, "share", "sub"))
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	junction(t, filepath.Join(home, "share"), filepath.Join(tmp, "via"))
	junction(t, filepath.Join(home, ".ssh"), filepath.Join(tmp, "keys"))

	for typed, want := range map[string]string{
		filepath.Join(tmp, "via"):        filepath.Join(realHome, "share"),
		filepath.Join(tmp, "via", "sub"): filepath.Join(realHome, "share", "sub"),
	} {
		if root, err := CheckRoot(typed, true); err != nil || root.Path != want || root.Shown != want {
			t.Errorf("CheckRoot(%s) = %+v, %v; want Path %s", typed, root, err, want)
		}
	}
	want := "refusing to share " + filepath.Join(realHome, ".ssh") + ": .ssh folders are never shared (key folder)"
	if _, err := CheckRoot(filepath.Join(tmp, "keys"), false); err == nil || err.Error() != want {
		t.Errorf("CheckRoot(keys) = %v\nwant %s", err, want)
	}
}

// A profile folder moved with a junction: both the folder holding the
// junction (C:\Users) and the one holding the profile contain home.
func TestCheckRootJunctionHome(t *testing.T) {
	tmp := t.TempDir()
	moved := filepath.Join(tmp, "D", "Profiles", "me")
	users := filepath.Join(tmp, "C", "Users")
	mkdirs(t, filepath.Join(moved, "Documents"), users)
	junction(t, moved, filepath.Join(users, "me"))
	t.Setenv("USERPROFILE", filepath.Join(users, "me"))
	t.Setenv("HOME", filepath.Join(users, "me"))
	for _, p := range []string{users, filepath.Dir(moved)} {
		if _, err := CheckRoot(p, false); err == nil || !strings.HasSuffix(err.Error(), ": it contains your home folder") {
			t.Errorf("CheckRoot(%s) = %v, want: it contains your home folder", p, err)
		}
	}
	if _, err := CheckRoot(filepath.Join(users, "me"), false); err == nil || !strings.HasPrefix(err.Error(), "refusing to share your home folder") {
		t.Errorf("CheckRoot(home) = %v", err)
	}
	if _, err := CheckRoot(filepath.Join(users, "me", "Documents"), true); err != nil {
		t.Errorf("CheckRoot(Documents) = %v", err)
	}
}

func TestCheckRootSystemRoot(t *testing.T) {
	tmp := t.TempDir()
	win := filepath.Join(tmp, "Windows")
	mkdirs(t, filepath.Join(win, "System32"), filepath.Join(tmp, "dev", "app"))
	t.Setenv("SystemRoot", win)
	for _, p := range []string{win, filepath.Join(win, "System32")} {
		if _, err := CheckRoot(p, false); err == nil || !strings.HasSuffix(err.Error(), ": it is a system folder") {
			t.Errorf("CheckRoot(%s) = %v, want: it is a system folder", p, err)
		}
	}
	if _, err := CheckRoot(filepath.Join(tmp, "dev", "app"), false); err != nil {
		t.Errorf("CheckRoot(dev\\app) = %v", err)
	}
}

func TestCheckRootLongPath(t *testing.T) {
	tmp := t.TempDir()
	want, err := filepath.EvalSymlinks(tmp) // long names for 8.3 ones like RUNNER~1
	if err != nil {
		t.Fatal(err)
	}
	deep := tmp
	for len(deep) < 300 {
		deep = filepath.Join(deep, "abcdefghijklmnop")
		want = filepath.Join(want, "abcdefghijklmnop")
	}
	mkdirs(t, deep)
	if root, err := CheckRoot(deep, false); err != nil || root.Path != want {
		t.Errorf("CheckRoot(%d bytes) = %+v, %v; want Path %s", len(deep), root, err, want)
	}
}

func TestDosPath(t *testing.T) {
	for in, want := range map[string]string{
		`\\?\C:\Users\me\share`:       `C:\Users\me\share`,
		`\\?\UNC\server\share\folder`: `\\server\share\folder`,
		`C:\plain`:                    `C:\plain`,
	} {
		if got := dosPath(in); got != want {
			t.Errorf("dosPath(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestLongPath(t *testing.T) {
	long := `C:\` + strings.Repeat(`folder\`, 40) + "share"
	unc := `\\server\share\` + strings.Repeat(`folder\`, 40) + "x"
	for in, want := range map[string]string{
		`C:\short`:         `C:\short`,
		long:               `\\?\` + long,
		unc:                `\\?\UNC\` + unc[2:],
		`\\?\` + long:      `\\?\` + long,
		`\\.\` + long[3:]:  `\\.\` + long[3:],
		`\\server\share\x`: `\\server\share\x`,
	} {
		if got := longPath(in); got != want {
			t.Errorf("longPath(%s) = %s, want %s", in, got, want)
		}
	}
}

// Suggestions in refusals must work when pasted into cmd.exe, which doesn't
// expand ~: they use the full path, quoted when it has spaces.
func TestExampleWindows(t *testing.T) {
	if got := example(`C:\Users\me`, "Documents", "to-share"); got != `C:\Users\me\Documents\to-share` {
		t.Errorf("example = %s", got)
	}
	if got := example(`C:\Users\Jane Doe`, "Desktop", "to-share"); got != `"C:\Users\Jane Doe\Desktop\to-share"` {
		t.Errorf("example with a space = %s", got)
	}
}
