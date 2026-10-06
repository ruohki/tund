package fileserve

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCheckRoot(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	key := keyPEM(t)
	for p, content := range map[string]string{
		"sub/notes.txt": "n", "sub/.env": "S=1", "sub/id_ed25519": "k", "sub/id_ed25519.pub": "ssh-ed25519 AAAA",
		"sub/key.pem": key, "sub/cert.pem": certPEM, "sub/README": "r", "sub/innocent.txt": "my key:\n" + key,
		".ssh/config": "Host x", ".config/x/y": "y", "work/.git/config": "[core]", "secrets/a.txt": "a", "Deck.key/Index.zip": "PK",
		".myapp/x": "x",
	} {
		full := filepath.Join(home, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	realHome, _ := filepath.EvalSymlinks(home)
	realTmp, _ := filepath.EvalSymlinks(tmp)
	tl := func(rel string) string { // what messages show for a path in home
		if runtime.GOOS == "windows" {
			return filepath.Join(realHome, filepath.FromSlash(rel))
		}
		return "~/" + rel
	}
	symlink := func(target, name string) string {
		p := filepath.Join(tmp, name)
		if err := os.Symlink(target, p); err != nil {
			return ""
		}
		return p
	}

	for _, c := range []struct {
		path string
		dir  bool
	}{
		{filepath.Join(home, "sub"), true},
		{filepath.Join(home, "Deck.key"), true},
		{filepath.Join(home, "sub", "id_ed25519.pub"), false},
		{filepath.Join(home, "sub", "cert.pem"), false},
		{filepath.Join(home, "sub", "README"), false},
	} {
		root, err := CheckRoot(c.path, true)
		rel := strings.TrimPrefix(c.path, home)
		want := Root{Path: filepath.Join(realHome, rel), Shown: tl(filepath.ToSlash(rel[1:])), Dir: c.dir, Name: filepath.Base(c.path)}
		if err != nil || root != want {
			t.Errorf("CheckRoot(%s) = %+v, %v; want %+v", c.path, root, err, want)
		}
	}

	for _, c := range []struct{ path, want string }{
		{home, "refusing to share your home folder (~): it holds app data, browser profiles and keychains that can't be told apart from secrets. Share a folder inside it, e.g. tund serve ~/Documents/to-share --password …"},
		{tmp, "refusing to share " + realTmp + ": it contains your home folder"},
		{filepath.Join(home, ".ssh"), "refusing to share " + tl(".ssh") + ": .ssh folders are never shared (key folder)"},
		{filepath.Join(home, ".ssh", "config"), "refusing to share " + tl(".ssh/config") + ": it is inside .ssh, which is never shared"},
		{filepath.Join(home, ".config", "x"), "refusing to share " + tl(".config/x") + ": it is inside .config, which is never shared"},
		{filepath.Join(home, "work", ".git"), "refusing to share " + tl("work/.git") + ": .git folders are never shared (version control)"},
		{filepath.Join(home, "secrets"), "refusing to share " + tl("secrets") + ": secrets folders are never shared (secret folder)"},
		{filepath.Join(home, "secrets", "a.txt"), "refusing to share " + tl("secrets/a.txt") + ": it is inside secrets, which is never shared"},
		{filepath.Join(home, ".myapp"), "refusing to share " + tl(".myapp") + ": dot folders are never shared"},
		{filepath.Join(home, "sub", ".env"), "refusing to share .env: secret files like .env, keys and credentials are never shared"},
		{filepath.Join(home, "sub", "id_ed25519"), "refusing to share id_ed25519: secret files like .env, keys and credentials are never shared"},
		{filepath.Join(home, "sub", "key.pem"), "refusing to share key.pem: it looks like it contains a secret ("},
		{filepath.Join(home, "sub", "innocent.txt"), "refusing to share innocent.txt: it looks like it contains a secret ("},
		{filepath.Join(home, "nope"), filepath.Join(home, "nope") + " does not exist"},
		{"3000", "3000 does not exist"},
		{symlink(home, "home-link"), "refusing to share your home folder (~)"},
		{symlink(filepath.Join(home, "sub", ".env"), "env-link.txt"), "refusing to share .env: secret files"},
		{symlink(filepath.Join(home, "sub", "README"), "id_rsa"), "refusing to share id_rsa: secret files"},
	} {
		if c.path == "" {
			continue // no symlinks here
		}
		_, err := CheckRoot(c.path, false)
		if err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("CheckRoot(%s) = %v\nwant %s", c.path, err, c.want)
		}
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]string{
			"/":         "refusing to share / (the whole disk): pick a folder with just the files to share, e.g. tund serve ~/Desktop/to-share --password …",
			"/dev":      "refusing to share /dev: it is a system folder",
			"/dev/null": "/dev/null is not a regular file or folder",
		} {
			if _, err := CheckRoot(path, false); err == nil || err.Error() != want {
				t.Errorf("CheckRoot(%s) = %v, want %s", path, err, want)
			}
		}
	}
}

// Suggestions in refusals use ~ where shells expand it (see
// TestExampleWindows for cmd.exe).
func TestExample(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TestExampleWindows")
	}
	if got := example("/home/me", "Documents", "to-share"); got != "~/Documents/to-share" {
		t.Errorf("example = %s", got)
	}
}

func TestSystemFolders(t *testing.T) {
	want := []string{"/proc", "/sys", "/dev"}
	if runtime.GOOS == "windows" {
		// Not "/dev": that is <current drive>:\dev there, a common home for code.
		want = []string{`D:\Windows`}
		t.Setenv("SystemRoot", want[0])
	}
	if got := systemFolders(); !slices.Equal(got, want) {
		t.Errorf("systemFolders() = %q, want %q", got, want)
	}
}

// A home folder that is a link (a profile moved with a junction on Windows):
// the folder holding the link and the one holding its target both contain it.
func TestCheckRootLinkedHome(t *testing.T) {
	tmp := t.TempDir()
	moved := filepath.Join(tmp, "disk", "me")
	users := filepath.Join(tmp, "users")
	for _, d := range []string{filepath.Join(moved, "docs"), users} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	symlink(t, moved, filepath.Join(users, "me"))
	t.Setenv("HOME", filepath.Join(users, "me"))
	t.Setenv("USERPROFILE", filepath.Join(users, "me"))
	for _, p := range []string{users, filepath.Dir(moved)} {
		if _, err := CheckRoot(p, false); err == nil || !strings.HasSuffix(err.Error(), ": it contains your home folder") {
			t.Errorf("CheckRoot(%s) = %v, want: it contains your home folder", p, err)
		}
	}
	if _, err := CheckRoot(filepath.Join(users, "me", "docs"), true); err != nil {
		t.Errorf("CheckRoot(~/docs) = %v", err)
	}
}
