package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"tund/internal/protocol"
	"tund/internal/tlsutil"
)

// Self-update: the server publishes the newest client under
// /_tund/downloads/ (version.txt, checksums.txt and the binaries, usually
// redirects to the GitHub release).

const downloadsPath = "/_tund/downloads/"

// maxBinarySize guards against a runaway download.
const maxBinarySize = 256 << 20

func updateHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tlsutil.MustClientConfig()}}
}

// LatestVersion asks the server for the newest client release ("0.4.0").
func LatestVersion(ctx context.Context, server string, hc *http.Client) (string, error) {
	if hc == nil {
		hc = updateHTTPClient()
	}
	body, err := fetch(ctx, hc, server, "version.txt", 64)
	if err != nil {
		return "", err
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(body)), "v")
	if _, ok := protocol.ParseVersion(v); !ok {
		return "", fmt.Errorf("%s published %q as the latest version", displayHost(server), v)
	}
	return v, nil
}

// fetch GETs a file from the server's downloads.
func fetch(ctx context.Context, hc *http.Client, server, name string, limit int64) ([]byte, error) {
	u, err := EndpointURL(server, downloadsPath+name)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tund/"+Version)
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("cannot reach %s: %w", displayHost(server), unwrapDial(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound && name == "version.txt":
		return nil, fmt.Errorf("%s doesn't publish client versions (is it up to date?)", displayHost(server))
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("downloading %s from %s: HTTP %d", name, displayHost(server), resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", name, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than expected", name)
	}
	return b, nil
}

// UpdateOptions configure Update.
type UpdateOptions struct {
	Server string
	// Force reinstalls even when this build is current (or a development build).
	Force bool
	// Executable to replace; default: the running binary.
	Executable string
	HTTPClient *http.Client
	// Progress, if set, receives one-line status messages.
	Progress func(string)
}

// UpdateResult describes a finished Update.
type UpdateResult struct {
	From, To string
	Path     string
	Updated  bool // false: already up to date
}

// ErrDevBuild: this binary wasn't built from a release, so there is nothing
// to compare with.
var ErrDevBuild = errors.New("this is a development build")

// BinaryName is the release asset for this OS and architecture.
func BinaryName() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64":
		return "tund-" + runtime.GOOS + "-" + runtime.GOARCH, nil
	case "windows/amd64", "windows/arm64":
		return "tund-windows-" + runtime.GOARCH + ".exe", nil
	}
	return "", fmt.Errorf("no tund release for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// Update installs the newest client release over the executable: download
// through the server, check the SHA-256 from checksums.txt, run it once, then
// swap it into place.
func Update(ctx context.Context, opts UpdateOptions) (*UpdateResult, error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = updateHTTPClient()
	}
	exe := opts.Executable
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return nil, fmt.Errorf("cannot find the tund executable: %w", err)
		}
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	name, err := BinaryName()
	if err != nil {
		return nil, err
	}

	latest, err := LatestVersion(ctx, opts.Server, hc)
	if err != nil {
		return nil, err
	}
	res := &UpdateResult{From: Version, To: latest, Path: exe}
	if !opts.Force {
		if _, ok := protocol.ParseVersion(Version); !ok {
			return nil, fmt.Errorf("%w (%s); pass --force to replace it with tund %s", ErrDevBuild, Version, latest)
		}
		if !protocol.NewerVersion(latest, Version) {
			return res, nil
		}
	}

	// Stage next to the executable so the final rename stays on one file
	// system; this also finds out early whether we may write there.
	pattern := ".tund-update-*"
	if runtime.GOOS == "windows" {
		pattern += ".exe" // Windows only runs files with the extension
	}
	tmp, err := os.CreateTemp(filepath.Dir(exe), pattern)
	if err != nil {
		return nil, notWritable(exe, err)
	}
	defer os.Remove(tmp.Name()) // no-op once renamed into place
	defer tmp.Close()

	sums, err := fetch(ctx, hc, opts.Server, "checksums.txt", 64<<10)
	if err != nil {
		return nil, err
	}
	want := checksumFor(sums, name)
	if want == "" {
		return nil, fmt.Errorf("checksums.txt has no entry for %s", name)
	}

	progress(fmt.Sprintf("Downloading tund %s for %s/%s …", latest, runtime.GOOS, runtime.GOARCH))
	bin, err := fetch(ctx, hc, opts.Server, name, maxBinarySize)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bin)
	if have := hex.EncodeToString(sum[:]); have != want {
		return nil, fmt.Errorf("checksum mismatch for %s (expected %s, got %s); nothing was changed", name, want, have)
	}
	if _, err := tmp.Write(bin); err != nil {
		return nil, fmt.Errorf("writing the new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("writing the new binary: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return nil, err
	}

	// Run it once: a binary that can't start must not replace a working one.
	vctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, tmp.Name(), "version").Output()
	if err != nil || !bytes.HasPrefix(out, []byte("tund "+latest)) {
		return nil, fmt.Errorf("the downloaded tund %s doesn't run on this machine (%v %q); nothing was changed", latest, err, strings.TrimSpace(string(out)))
	}

	if err := replaceExecutable(tmp.Name(), exe); err != nil {
		return nil, notWritable(exe, err)
	}
	res.Updated = true
	return res, nil
}

// checksumFor finds name in `sha256sum` output.
func checksumFor(sums []byte, name string) string {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

// replaceExecutable moves src over dst. Windows can't overwrite a running
// executable but can rename it, so the old one steps aside as dst.old
// (removed by CleanupOldExecutable on a later run).
func replaceExecutable(src, dst string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(src, dst)
	}
	old := dst + ".old"
	_ = os.Remove(old)
	if err := os.Rename(dst, old); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Rename(old, dst)
		return err
	}
	return nil
}

// CleanupOldExecutable removes what a Windows update left behind.
func CleanupOldExecutable() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}
}

// PermissionError: the executable's directory isn't writable for this user.
type PermissionError struct{ Path string }

func (e *PermissionError) Error() string { return "cannot replace " + e.Path + ": permission denied" }

func notWritable(exe string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return &PermissionError{Path: exe}
	}
	return fmt.Errorf("cannot replace %s: %w", exe, err)
}
