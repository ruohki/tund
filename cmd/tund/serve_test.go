package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"tund/internal/client"
	"tund/internal/fileserve"
	"tund/internal/protocol"
)

// generated matches the passwords tund suggests.
var generated = regexp.MustCompile(`^[a-hjkmnp-z2-9]{4}-[a-hjkmnp-z2-9]{4}-[a-hjkmnp-z2-9]{4}$`)

// testHome makes a home folder holding files (slash-separated path →
// content) and points HOME at it.
func testHome(t *testing.T, files map[string]string) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	for p, content := range files {
		full := filepath.Join(home, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	return home
}

// testPEM returns a self-signed certificate and its private key as PEM.
func testPEM(t *testing.T) (cert, key string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "files.example.com"},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))
}

// rejectingServer is a tund server that counts requests and turns them all
// away, which ends a client right after it connected.
func rejectingServer(t *testing.T) (url string, hits *atomic.Int32) {
	t.Helper()
	hits = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, `{"error":"invalid authtoken"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, hits
}

// runCLI runs tund with args against server, with its own config file.
func runCLI(t *testing.T, server string, args ...string) error {
	t.Helper()
	cmd := newRootCmd()
	cmd.SetArgs(slices.Concat(args, []string{"--server", server, "--authtoken", "tund_test",
		"--config", filepath.Join(t.TempDir(), "tund.yml"), "--log"}))
	return cmd.Execute()
}

// suggested returns the password at the end of a suggestion line, or "".
func suggested(t *testing.T, line, prefix string) string {
	t.Helper()
	pw, ok := strings.CutPrefix(line, prefix)
	if !ok || !generated.MatchString(pw) {
		t.Errorf("no generated password after %q in %q", prefix, line)
		return ""
	}
	return pw
}

func TestServeNeedsAuth(t *testing.T) {
	home := testHome(t, map[string]string{"share/a.txt": "a", "Q3 Assets/a.txt": "a", "it's/a.txt": "a"})
	t.Chdir(home)
	quoted := map[string]string{"share": "share", "./share/": "./share/", "Q3 Assets": "'Q3 Assets'", "it's": `'it'\''s'`}
	if runtime.GOOS == "windows" {
		quoted["Q3 Assets"], quoted["it's"] = `"Q3 Assets"`, `"it's"` // for cmd.exe and PowerShell
	}
	seen := map[string]bool{}
	for path, q := range quoted {
		_, err := checkServe(path, serveFlags{})
		// Each explanation has a line of its own: cmd.exe, and zsh by
		// default, pass a trailing # comment on as arguments.
		lines := strings.Split(fmt.Sprint(err), "\n")
		if len(lines) != 6 || lines[0] != "tund serve needs --password or --oidc: shared files are never public" || lines[1] != "" ||
			lines[2] != "Share it with this random password, or your own:" ||
			lines[4] != "Or let visitors sign in with an OIDC provider from the dashboard:" ||
			lines[5] != "  tund serve "+q+" --oidc <provider> --oidc-allow @example.com" {
			t.Errorf("checkServe(%q) = %v", path, err)
			continue
		}
		pw := suggested(t, lines[3], "  tund serve "+q+" --password ")
		if seen[pw] {
			t.Errorf("password %q suggested twice", pw)
		}
		seen[pw] = true
	}
	// An allow list alone protects nothing.
	if _, err := checkServe("share", serveFlags{allow: []string{"@example.com"}}); err == nil || !strings.HasPrefix(err.Error(), "tund serve needs --password or --oidc") {
		t.Errorf("--oidc-allow only: %v", err)
	}
}

func TestServeChecks(t *testing.T) {
	home := testHome(t, map[string]string{"share/report.pdf": "%PDF-1.7"})
	dir, file := filepath.Join(home, "share"), filepath.Join(home, "share", "report.pdf")
	const pinNeedsName = "--pin needs --subdomain or --domain: a share already keeps its random hostname for its path"
	for _, c := range []struct {
		path string
		f    serveFlags
		want string // the error; a trailing "--password " is followed by a generated one
	}{
		{dir, serveFlags{password: "s3cret"}, "use a password with at least 8 characters (or omit --password to get a suggested one)"},
		{dir, serveFlags{password: "pässwör"}, "use a password with at least 8 characters (or omit --password to get a suggested one)"},
		{dir, serveFlags{password: "s3cret-share", oidc: "google"}, "use either a password or OIDC, not both"},
		{dir, serveFlags{password: "s3cret-share", allow: []string{"@example.com"}}, "an allow list only applies to OIDC"},
		{dir, serveFlags{oidc: "acme/okta"}, "--oidc needs --oidc-allow: who may open the share, e.g. --oidc-allow @example.com (emails, @domains or group:<name>), or --oidc-allow '*' for anyone who can sign in to acme/okta"},
		{dir, serveFlags{oidc: "google", allow: []string{" , "}}, "--oidc needs --oidc-allow: who may open the share, e.g. --oidc-allow @example.com (emails, @domains or group:<name>), or --oidc-allow '*' for anyone who can sign in to google"},
		{dir, serveFlags{oidc: "google", allow: []string{"*,@example.com"}}, "--oidc-allow '*' lets in anyone who can sign in: use it on its own, not with other entries"},
		{dir, serveFlags{oidc: "google", allow: []string{"@example.com", "*"}}, "--oidc-allow '*' lets in anyone who can sign in: use it on its own, not with other entries"},
		{dir, serveFlags{oidc: "google", allow: []string{"'*'", "@example.com"}}, "--oidc-allow '*' lets in anyone who can sign in: use it on its own, not with other entries"},
		{file, serveFlags{password: "s3cret-share", upload: true}, "--upload needs a folder: visitors can only download a single shared file (report.pdf)\n\nTo collect files, share a folder:\n  tund serve ./inbox --upload --password "},
		{dir, serveFlags{password: "s3cret-share", subdomain: "files", domain: "files.example.com"}, "use either --subdomain or --domain, not both"},
		{dir, serveFlags{password: "s3cret-share", random: true, subdomain: "files"}, "--random cannot be combined with --subdomain or --domain"},
		{dir, serveFlags{password: "s3cret-share", random: true, domain: "files.example.com"}, "--random cannot be combined with --subdomain or --domain"},
		// Pinning a random hostname would make it the account's default.
		{dir, serveFlags{password: "s3cret-share", pin: true}, pinNeedsName},
		{dir, serveFlags{password: "s3cret-share", pin: true, random: true}, pinNeedsName},
		{dir, serveFlags{oidc: "google", allow: []string{"*"}, pin: true, subdomain: " "}, pinNeedsName},

		// The order: share root, --upload with a file, password or single sign-on, hostname.
		{filepath.Join(home, "nope"), serveFlags{upload: true}, filepath.Join(home, "nope") + " does not exist"},
		{file, serveFlags{upload: true, oidc: "google"}, "--upload needs a folder: visitors can only download a single shared file (report.pdf)\n\nTo collect files, share a folder:\n  tund serve ./inbox --upload --password "},
		{dir, serveFlags{password: "short", subdomain: "files", domain: "files.example.com"}, "use a password with at least 8 characters (or omit --password to get a suggested one)"},
	} {
		_, err := checkServe(c.path, c.f)
		msg := fmt.Sprint(err)
		if prefix, ok := strings.CutSuffix(c.want, "--password "); ok {
			if err == nil || !strings.HasPrefix(msg, prefix) {
				t.Errorf("checkServe(%s, %+v) = %v\nwant %s", c.path, c.f, err, c.want)
			} else {
				suggested(t, msg[strings.LastIndexByte(msg, '\n')+1:], "  tund serve ./inbox --upload --password ")
			}
			continue
		}
		if err == nil || msg != c.want {
			t.Errorf("checkServe(%s, %+v) = %v\nwant %s", c.path, c.f, err, c.want)
		}
	}
}

func TestServeRefusesRoots(t *testing.T) {
	home := testHome(t, map[string]string{"sub/a.txt": "a", ".ssh/config": "Host x", ".config/x/y": "y"})
	refused := map[string]string{
		home:                                  "refusing to share your home folder (~): it holds app data, browser profiles and keychains",
		filepath.Dir(home):                    ": it contains your home folder",
		filepath.Join(home, ".ssh", "config"): ": it is inside .ssh, which is never shared",
		filepath.Join(home, ".config", "x"):   ": it is inside .config, which is never shared",
	}
	if runtime.GOOS != "windows" {
		refused["/"] = "refusing to share / (the whole disk): pick a folder with just the files to share"
	}
	for path, want := range refused {
		// Refused before any password is asked for.
		for _, f := range []serveFlags{{password: "s3cret-share"}, {}} {
			if _, err := checkServe(path, f); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("checkServe(%s, %+v) = %v\nwant %s", path, f, err, want)
			}
		}
	}
	sh, err := checkServe(filepath.Join(home, "sub"), serveFlags{password: "s3cret-share"})
	if err != nil {
		t.Fatalf("HOME/sub: %v", err)
	}
	if runtime.GOOS != "windows" && sh.spec.Display != "~/sub" {
		t.Errorf("Display = %q, want ~/sub", sh.spec.Display)
	}
}

func TestServeSecretFiles(t *testing.T) {
	cert, key := testPEM(t)
	home := testHome(t, map[string]string{
		"keys/.env": "TOKEN=1", "keys/id_ed25519": "-", "keys/key.pem": key,
		"keys/id_ed25519.pub": "ssh-ed25519 AAAA", "keys/cert.pem": cert,
	})
	for name, want := range map[string]string{
		".env":           "refusing to share .env: secret files like .env, keys and credentials are never shared",
		"id_ed25519":     "refusing to share id_ed25519: secret files like .env, keys and credentials are never shared",
		"key.pem":        "refusing to share key.pem: it looks like it contains a secret (",
		"id_ed25519.pub": "",
		"cert.pem":       "",
	} {
		_, err := checkServe(filepath.Join(home, "keys", name), serveFlags{password: "s3cret-share"})
		if want == "" && err != nil || want != "" && (err == nil || !strings.HasPrefix(err.Error(), want)) {
			t.Errorf("checkServe(%s) = %v, want %q", name, err, want)
		}
	}
}

func TestServeMissingPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".cache", "2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	cases := map[string]string{
		"./nope":  "./nope does not exist",
		"3000":    "3000 does not exist; to expose a local server on port 3000, use: tund http 3000",
		"99999":   "99999 does not exist",
		"2024-q3": "2024-q3 does not exist",
	}
	// A link to nothing is missing too.
	if err := os.Symlink("gone", filepath.Join(dir, "3001")); err == nil {
		cases["3001"] = "3001 does not exist; to expose a local server on port 3001, use: tund http 3001"
	} else {
		t.Logf("no symlinks here: %v", err)
	}
	for path, want := range cases {
		if _, err := checkServe(path, serveFlags{password: "s3cret-share"}); err == nil || err.Error() != want {
			t.Errorf("checkServe(%s) = %v, want %q", path, err, want)
		}
	}
	// A folder named like a year that is refused for another reason is no port.
	t.Chdir(filepath.Join(dir, ".cache"))
	if _, err := checkServe("2024", serveFlags{password: "s3cret-share"}); err == nil ||
		!strings.Contains(err.Error(), "it is inside .cache, which is never shared") || strings.Contains(err.Error(), "tund http") {
		t.Errorf("checkServe(.cache/2024) = %v", err)
	}
}

func TestServeSpec(t *testing.T) {
	home := testHome(t, map[string]string{"Q3 Assets (final)/a.txt": "a", "Q3 Assets (final)/report.pdf": strings.Repeat("x", 1536)})
	dir := filepath.Join(home, "Q3 Assets (final)")
	file := filepath.Join(dir, "report.pdf")
	shown, shownFile := "~/Q3 Assets (final)", "~/Q3 Assets (final)/report.pdf"
	if runtime.GOOS == "windows" {
		root, err := fileserve.CheckRoot(dir, false)
		if err != nil {
			t.Fatal(err)
		}
		shown, shownFile = root.Shown, filepath.Join(root.Shown, "report.pdf")
	}
	const (
		hidden = "hidden from visitors: dotfiles and secret files (.env, keys, credentials)"
		anyone = "anyone who can sign in to google can open this share"
	)
	readOnly := []string{"read-only: visitors can browse and download files", hidden}
	pw := &protocol.Auth{Mode: protocol.AuthPassword, Password: "s3cret-share"}
	anyoneSpec := client.TunnelSpec{Name: "serve-q3-assets-final", Display: shown, Auth: &protocol.Auth{Mode: protocol.AuthOIDC, Provider: "google"},
		Random: true, Notes: append(readOnly, anyone)}
	for _, c := range []struct {
		path    string
		f       serveFlags
		want    client.TunnelSpec
		warning string
	}{
		{dir, serveFlags{password: "s3cret-share"},
			client.TunnelSpec{Name: "serve-q3-assets-final", Display: shown, Auth: pw, Random: true, Notes: readOnly}, ""},
		{dir, serveFlags{password: "s3cret-share", upload: true},
			client.TunnelSpec{Name: "serve-q3-assets-final", Display: shown, Auth: pw, Random: true, Badge: "upload",
				Notes: []string{"visitors can browse, download and upload files · uploads never replace existing files", hidden}}, ""},
		{file, serveFlags{password: "s3cret-share"},
			client.TunnelSpec{Name: "serve-report-pdf", Display: shownFile, Auth: pw, Random: true,
				Notes: []string{"visitors get a download page for report.pdf (1.5 KB)"}}, ""},
		{dir, serveFlags{password: "s3cret-share", name: "assets", subdomain: " Files ", pin: true, restart: true, allowIPs: []string{"203.0.113.7"}},
			client.TunnelSpec{Name: "assets", Display: shown, Subdomain: "files", Auth: pw, Pin: true, AllowIPs: []string{"203.0.113.7"},
				RestartOnExpiry: true, Notes: readOnly}, ""},
		{dir, serveFlags{password: "s3cret-share", domain: "Files.Example.com"},
			client.TunnelSpec{Name: "serve-q3-assets-final", Display: shown, Hostname: "files.example.com", Auth: pw, Notes: readOnly}, ""},
		{dir, serveFlags{password: "s3cret-share", domain: "files.example.com", pin: true},
			client.TunnelSpec{Name: "serve-q3-assets-final", Display: shown, Hostname: "files.example.com", Auth: pw, Pin: true, Notes: readOnly}, ""},
		{dir, serveFlags{password: "s3cret-share", random: true},
			client.TunnelSpec{Name: "serve-q3-assets-final", Display: shown, Auth: pw, Random: true, Notes: readOnly}, ""},
		{dir, serveFlags{oidc: "google", allow: []string{"*"}}, anyoneSpec, anyone},
		// cmd.exe passes the quotes of '*' on.
		{dir, serveFlags{oidc: "google", allow: []string{"'*'"}}, anyoneSpec, anyone},
		{dir, serveFlags{oidc: "google", allow: []string{`"*"`}}, anyoneSpec, anyone},
		{dir, serveFlags{oidc: "acme/okta", allow: []string{"@Example.com,group:Design"}},
			client.TunnelSpec{Name: "serve-q3-assets-final", Display: shown, Random: true, Notes: readOnly,
				Auth: &protocol.Auth{Mode: protocol.AuthOIDC, Provider: "acme/okta", Allow: []string{"@example.com", "group:Design"}}}, ""},
	} {
		sh, err := checkServe(c.path, c.f)
		if err != nil {
			t.Errorf("checkServe(%s, %+v): %v", c.path, c.f, err)
			continue
		}
		c.want.LocalAddr = "http://file-share"
		if !reflect.DeepEqual(sh.spec, c.want) {
			t.Errorf("checkServe(%s, %+v) =\n%+v auth %+v\nwant\n%+v auth %+v", c.path, c.f, sh.spec, *sh.spec.Auth, c.want, *c.want.Auth)
		}
		if sh.warning != c.warning {
			t.Errorf("checkServe(%s, %+v) warns %q, want %q", c.path, c.f, sh.warning, c.warning)
		}
		// The client takes it once the share serves it.
		sh.spec.Handler = http.NotFoundHandler()
		if err := sh.spec.Validate(); err != nil {
			t.Errorf("Validate(%+v): %v", c.f, err)
		}
	}
}

func TestServeName(t *testing.T) {
	for base, want := range map[string]string{
		"Q3 Assets (final)":            "serve-q3-assets-final",
		"report.pdf":                   "serve-report-pdf",
		"--Photos 2024--":              "serve-photos-2024",
		"Café Bilder":                  "serve-caf-bilder",
		"...":                          "serve",
		"日本語":                          "serve",
		strings.Repeat("a", 40):        "serve-" + strings.Repeat("a", 26),
		strings.Repeat("a", 25) + " b": "serve-" + strings.Repeat("a", 25), // no dash left at the cut
	} {
		if got := serveName(base); got != want || len(got) > 32 {
			t.Errorf("serveName(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	for _, c := range []struct{ s, posix, windows string }{
		{"share", "share", "share"},
		{"./a/b-c_d.txt", "./a/b-c_d.txt", "./a/b-c_d.txt"},
		{"-x", "./-x", "./-x"},
		{"50%", "50%", "50%"},
		{"", "''", `""`},
		// Double quotes on Windows: cmd.exe passes single quotes on.
		{"Q3 Assets", "'Q3 Assets'", `"Q3 Assets"`},
		{"it's", `'it'\''s'`, `"it's"`},
		{"a*b", "'a*b'", `"a*b"`},
		{"Café", "'Café'", `"Café"`},
		{"50% off", "'50% off'", `"50% off"`},
		{"a,b", "a,b", `"a,b"`}, // PowerShell reads a,b as two arguments
		{`C:\Users\me\share`, `'C:\Users\me\share'`, `C:\Users\me\share`},
		{`C:\Users\me\Q3 Assets`, `'C:\Users\me\Q3 Assets'`, `"C:\Users\me\Q3 Assets"`},
		// PowerShell expands $ and ` inside double quotes.
		{"$HOME", "'$HOME'", "'$HOME'"},
		{"it's $5", `'it'\''s $5'`, "'it''s $5'"},
		{"a`b", "'a`b'", "'a`b'"},
	} {
		if got := quoteArg(c.s, false); got != c.posix {
			t.Errorf("quoteArg(%q) = %s, want %s", c.s, got, c.posix)
		}
		if got := quoteArg(c.s, true); got != c.windows {
			t.Errorf("quoteArg(%q) on Windows = %s, want %s", c.s, got, c.windows)
		}
	}
	if got, want := shellQuote("Q3 Assets"), quoteArg("Q3 Assets", runtime.GOOS == "windows"); got != want {
		t.Errorf("shellQuote = %s, want %s", got, want)
	}
}

func TestUploadEvent(t *testing.T) {
	u := fileserve.Upload{Path: "/inbox/a_b (1).txt", Requested: "a:b.txt", Size: 2457600, Renamed: true, Taken: "a_b.txt",
		User: "alice@example.com", Remote: "203.0.113.7"}
	want := client.UploadEvent{Path: "/inbox/a_b (1).txt", Requested: "a:b.txt", Size: 2457600, Renamed: true, Taken: "a_b.txt",
		User: "alice@example.com", Remote: "203.0.113.7"}
	if got := uploadEvent(u); got != want {
		t.Errorf("uploadEvent(saved) = %+v, want %+v", got, want)
	}
	u.Err, want.Error = fileserve.ErrDiskFull, "the disk is full"
	if got := uploadEvent(u); got != want {
		t.Errorf("uploadEvent(failed) = %+v, want %+v", got, want)
	}
}

// TestServeCommandChecksFirst runs tund serve itself: every check happens
// before it connects.
func TestServeCommandChecksFirst(t *testing.T) {
	server, hits := rejectingServer(t)
	home := testHome(t, map[string]string{"share/report.pdf": "%PDF-1.7"})
	t.Chdir(home)
	for _, c := range []struct {
		args []string
		want string
	}{
		// Not ".": that is the home folder in a new terminal.
		{[]string{"serve"}, "name the file or folder to share, e.g. tund serve ./to-share --password "},
		{[]string{"serve", ""}, "name the file or folder to share, e.g. tund serve ./to-share --password "},
		{[]string{"serve", "a", "b"}, "share one file or folder at a time, not 2: put the files in a folder (and quote paths with spaces)"},
		{[]string{"serve", "share"}, "tund serve needs --password or --oidc: shared files are never public"},
		{[]string{"serve", "share", "--oidc", "google"}, "--oidc needs --oidc-allow: "},
		{[]string{"serve", "share/report.pdf", "--upload"}, "--upload needs a folder: "},
		{[]string{"serve", ".", "--password", "s3cret-share"}, "refusing to share your home folder (~)"},
		{[]string{"serve", "3000", "--password", "s3cret-share"}, "3000 does not exist; to expose a local server on port 3000, use: tund http 3000"},
		{[]string{"serve", "share", "--password", "s3cret-share", "--pin"}, "--pin needs --subdomain or --domain"},
		// Past the serve checks, a bad --allow-ip still stops it before it connects.
		{[]string{"serve", "share", "--upload", "--password", "s3cret-share", "--allow-ip", "not-an-ip"}, `invalid IP address "not-an-ip"`},
	} {
		err := runCLI(t, server, c.args...)
		if err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("tund %q = %v, want %s", c.args, err, c.want)
		} else if strings.HasSuffix(c.want, "--password ") {
			suggested(t, err.Error(), c.want)
		}
	}
	if n := hits.Load(); n > 0 {
		t.Errorf("%d requests reached the server", n)
	}
}

// TestServeHelpExamples checks that every example in the help passes the
// checks of tund serve (a bad --allow-ip then stops it before it connects).
func TestServeHelpExamples(t *testing.T) {
	server, hits := rejectingServer(t)
	home := testHome(t, map[string]string{"dist/index.html": "<p>", "report.pdf": "%PDF-1.7", "inbox/a.txt": "a",
		"photos/a.jpg": "jpg", "team/a.txt": "a", "share/a.txt": "a"})
	t.Chdir(home)
	for _, line := range strings.Split(newServeCmd(&globals{}).Example, "\n") {
		line, _, _ = strings.Cut(line, "#")
		args := strings.Fields(line)[1:] // without "tund"
		for i, a := range args {
			a = strings.Trim(a, "'") // as the shell passes it
			if rest, ok := strings.CutPrefix(a, "~/"); ok {
				a = filepath.Join(home, rest)
			}
			args[i] = a
		}
		err := runCLI(t, server, append(args, "--allow-ip", "not-an-ip")...)
		if err == nil || !strings.HasPrefix(err.Error(), `invalid IP address "not-an-ip"`) {
			t.Errorf("%s: %v", strings.TrimSpace(line), err)
		}
	}
	if n := hits.Load(); n > 0 {
		t.Errorf("%d requests reached the server", n)
	}
}

// edgeSession is a client connected to fakeEdge, after its bind.
type edgeSession struct {
	*yamux.Session
	tunnel string // the bind's ID, which streams name
}

// fakeEdge is a tund server that answers the bind of every session with
// reply and hands the sessions it bound to the test.
func fakeEdge(t *testing.T, reply func(bind protocol.Message) protocol.Message) (url string, bound <-chan edgeSession) {
	t.Helper()
	sessions := make(chan edgeSession, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return
		}
		sess, err := yamux.Server(websocket.NetConn(context.Background(), ws, websocket.MessageBinary), protocol.YamuxConfig())
		if err != nil {
			return
		}
		defer sess.Close()
		st, err := sess.AcceptStream()
		if err != nil {
			return
		}
		ctl := protocol.NewControl(st)
		_ = ctl.Send(protocol.Message{Type: protocol.TypeWelcome, ServerVersion: "dev"})
		m, err := ctl.Recv()
		if err != nil || m.Type != protocol.TypeBind {
			return
		}
		res := reply(m)
		_ = ctl.Send(res)
		if res.Type == protocol.TypeBound {
			sessions <- edgeSession{sess, m.ID}
		}
		for {
			if _, err := ctl.Recv(); err != nil {
				return // the client hung up
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, sessions
}

// stream opens a data stream to the tunnel, as the edge does for a request.
func (es edgeSession) stream(t *testing.T) net.Conn {
	t.Helper()
	st, err := es.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := protocol.WriteStreamHeader(st, protocol.StreamHeader{Tunnel: es.tunnel}); err != nil {
		t.Fatal(err)
	}
	if err := protocol.ReadStreamStatus(st); err != nil {
		t.Fatal(err)
	}
	return st
}

// output collects what a Display printed, line by line without the time.
type output struct {
	mu    sync.Mutex
	lines []string
}

// logDisplay returns a Display for plain log lines and what it prints: a
// Display writes to the os.Stdout it was made with.
func logDisplay(t *testing.T) (*client.Display, *output) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	d := client.NewDisplay(true)
	os.Stdout = stdout
	out := &output{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			_, line, _ := strings.Cut(sc.Text(), " ") // the RFC 3339 time
			out.mu.Lock()
			out.lines = append(out.lines, line)
			out.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		w.Close()
		<-done
		r.Close()
	})
	return d, out
}

// wait waits for a line that matches pattern as a whole and returns its
// position.
func (o *output) wait(t *testing.T, pattern string) int {
	t.Helper()
	re := regexp.MustCompile("^(?:" + pattern + ")$")
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		o.mu.Lock()
		i := slices.IndexFunc(o.lines, re.MatchString)
		o.mu.Unlock()
		if i >= 0 {
			return i
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	t.Fatalf("no line like %s in:\n%s", pattern, strings.Join(o.lines, "\n"))
	return -1
}

// watchShares hands the test every share serveShare opens.
func watchShares(t *testing.T) <-chan *fileserve.Server {
	opened := make(chan *fileserve.Server, 4)
	openShare = func(o fileserve.Options) (*fileserve.Server, error) {
		s, err := fileserve.New(o)
		if err == nil {
			opened <- s
		}
		return s, err
	}
	t.Cleanup(func() { openShare = fileserve.New })
	return opened
}

// isClosed reports whether a share answers like a closed one: 503.
func isClosed(s *fileserve.Server) bool {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Tund-Auth", "password")
	s.ServeHTTP(w, r)
	return w.Code == http.StatusServiceUnavailable
}

// tempFiles lists the uploads in progress in dir.
func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var temps []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tund-upload-") {
			temps = append(temps, e.Name())
		}
	}
	return temps
}

// TestServeShare runs tund serve against a fake edge: uploads through the
// tunnel show up in the terminal, and Ctrl+C stops an upload in progress,
// which leaves nothing behind, before the tunnel goes down.
func TestServeShare(t *testing.T) {
	home := testHome(t, map[string]string{"share/inbox/old.txt": "old"})
	inbox := filepath.Join(home, "share", "inbox")
	f := serveFlags{password: "s3cret-share", upload: true}
	sh, err := checkServe(filepath.Join(home, "share"), f)
	if err != nil {
		t.Fatal(err)
	}
	opened := watchShares(t)
	url, bound := fakeEdge(t, func(m protocol.Message) protocol.Message {
		return protocol.Message{Type: protocol.TypeBound, ID: m.ID, URL: "https://calm-owl-7.tund.example", AuthMode: protocol.AuthPassword}
	})
	g := &globals{server: url, authtoken: "tund_test", configPath: filepath.Join(t.TempDir(), "tund.yml"), logMode: true}
	display, out := logDisplay(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveShare(ctx, g, f, sh, display) }()
	var es edgeSession
	select {
	case es = <-bound:
	case err := <-done:
		t.Fatalf("serveShare = %v before the bind", err)
	case <-time.After(10 * time.Second):
		t.Fatal("no bind")
	}

	st := es.stream(t)
	req, _ := http.NewRequest(http.MethodPut, "http://calm-owl-7.tund.example/inbox/notes.txt", strings.NewReader("hello"))
	req.Header.Set("X-Tund-Auth", "password")
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	if err := req.Write(st); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(st), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if b, _ := os.ReadFile(filepath.Join(inbox, "notes.txt")); resp.StatusCode != http.StatusCreated || string(b) != "hello" {
		t.Fatalf("PUT = %s, saved %q", resp.Status, b)
	}
	out.wait(t, `upload saved path=/inbox/notes\.txt size=5 remote=203\.0\.113\.7`)

	// Ctrl+C while a visitor is in the middle of an upload.
	st = es.stream(t)
	fmt.Fprintf(st, "PUT /inbox/big.bin HTTP/1.1\r\nHost: calm-owl-7.tund.example\r\nX-Tund-Auth: password\r\nX-Forwarded-For: 203.0.113.7\r\nContent-Length: %d\r\n\r\n", 1<<20)
	if _, err := st.Write(make([]byte, 100<<10)); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); len(tempFiles(t, inbox)) == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the upload never started")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serveShare after Ctrl+C = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveShare still runs after Ctrl+C")
	}
	stopped := out.wait(t, `upload failed path=/inbox/big\.bin received=\d+ remote=203\.0\.113\.7 error="the share stopped"`)
	if down := out.wait(t, "stopped"); down < stopped {
		t.Errorf("the tunnel went down (line %d) before the upload stopped (line %d)", down, stopped)
	}
	if left := tempFiles(t, inbox); left != nil {
		t.Errorf("temp files left: %v", left)
	}
	if s := <-opened; !isClosed(s) {
		t.Error("the share is still open after Ctrl+C")
	}

	// A run that ends with an error closes the share all the same. Log
	// lines show no notes, so the warning about '*' gets one of its own.
	sh, err = checkServe(filepath.Join(home, "share"), serveFlags{oidc: "google", allow: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	url, _ = fakeEdge(t, func(m protocol.Message) protocol.Message {
		return protocol.Message{Type: protocol.TypeBindError, ID: m.ID, Error: "no hostname left"}
	})
	g = &globals{server: url, authtoken: "tund_test", configPath: filepath.Join(t.TempDir(), "tund.yml"), logMode: true}
	display, out = logDisplay(t)
	if err := serveShare(context.Background(), g, serveFlags{}, sh, display); err == nil || err.Error() != "no tunnel could be started" {
		t.Errorf("serveShare with a refused bind = %v", err)
	}
	if warned, connecting := out.wait(t, `warning msg="anyone who can sign in to google can open this share"`), out.wait(t, "connecting .*"); warned > connecting {
		t.Error("the warning came after connecting")
	}
	select {
	case s := <-opened:
		if !isClosed(s) {
			t.Error("the share is still open after the run failed")
		}
	default:
		t.Error("no share opened")
	}
}
