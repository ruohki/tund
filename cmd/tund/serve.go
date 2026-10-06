package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"tund/internal/client"
	"tund/internal/fileserve"
	"tund/internal/protocol"
)

// serveFlags are the options of tund serve.
type serveFlags struct {
	name, subdomain, domain, password, oidc string
	allow, allowIPs                         []string
	upload, pin, random, restart            bool
}

func newServeCmd(g *globals) *cobra.Command {
	var f serveFlags
	cmd := &cobra.Command{
		Use:   "serve <file | folder>",
		Short: "Share a file or folder, behind a password or single sign-on",
		Long: `Share a file or a folder from this machine on an HTTPS URL. Visitors get a
file browser: they open folders, view images, PDFs, audio, video and text,
and download files. A single file gets a download page.

Shared files are never public: pass --password, or --oidc with --oidc-allow
to let visitors sign in with an OIDC provider from the dashboard
(--oidc-allow '*' lets in anyone who can sign in there). tund serves the
files itself; nothing listens on a local port.

Visitors can't change anything unless you pass --upload (folders only).
Then they can add files to the folder and its subfolders. Uploads never
replace a file: a taken name gets a number, like "report (1).pdf". Nobody
can delete or rename files or create folders.

Dotfiles and secret files are never listed or served: .env files, SSH and
TLS keys, cloud credentials, password stores, .git folders and the like.
tund refuses to share / or your home folder.

A share gets a random hostname, which tund remembers for its path; use
--subdomain or --domain for a fixed one, and add --pin to keep it as a
static hostname of your account. Transfers show up in the inspector like
any other request, and the first part of each file is recorded there.

tund serve shares files; it doesn't run websites (HTML files download).
For a site, run a local web server and use tund http.

Scripts use curl with the password (any user name):
  curl -u :s3cret-share <url>/                      # list a folder
  curl -u :s3cret-share -OJ <url>/report.pdf        # download with the shared name
  curl -u :s3cret-share -T notes.txt <url>/inbox/   # upload into a folder (with --upload)
curl -OJ writes letters outside ASCII as _, and uploads can't be resumed
with -C: send the whole file again.`,
		Example: `  tund serve ./dist --password s3cret-share              # browse and download a folder
  tund serve report.pdf --password s3cret-share          # a download page for one file
  tund serve ~/inbox --upload --password s3cret-share    # visitors can also upload files
  tund serve ./photos --oidc google --oidc-allow @example.com
  tund serve ./team --oidc acme/okta --oidc-allow '*'    # anyone who can sign in to acme/okta
  tund serve ./share --subdomain files --pin --password s3cret-share   # keep https://files.<base domain>`,
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) == 0 || args[0] == "":
				// Not ".": new terminals start in the home folder, which is refused.
				return errors.New("name the file or folder to share, e.g. tund serve ./to-share --password " + client.RandomPassword())
			case len(args) > 1:
				return fmt.Errorf("share one file or folder at a time, not %d: put the files in a folder (and quote paths with spaces)", len(args))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			sh, err := checkServe(args[0], f)
			if err != nil {
				return err
			}
			ctx, stop := signalContext()
			defer stop()
			return serveShare(ctx, g, f, sh, client.NewDisplay(g.logMode))
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&f.upload, "upload", false, "let visitors upload files into the folder and its subfolders (folders only; existing files are never replaced)")
	fl.StringVar(&f.password, "password", "", "visitors must enter this password, at least 8 characters (this or --oidc is required)")
	fl.StringVar(&f.oidc, "oidc", "", "visitors sign in with an OIDC provider from the dashboard: <provider> or <team>/<provider> (this or --password is required)")
	fl.StringSliceVar(&f.allow, "oidc-allow", nil, "who may open the share with --oidc: emails (a@b.com), domains (@b.com), groups (group:admins) or '*' for anyone who can sign in; repeatable or comma separated")
	fl.StringVar(&f.name, "name", "", "tunnel name shown in the dashboard (default serve-<file or folder name>)")
	fl.StringVar(&f.subdomain, "subdomain", "", "subdomain of the server's base domain")
	fl.StringVar(&f.domain, "domain", "", "full hostname, e.g. a custom domain verified in the dashboard")
	fl.BoolVar(&f.pin, "pin", false, "with --subdomain or --domain: keep that hostname as a static hostname of your account")
	fl.BoolVar(&f.random, "random", false, "use a random hostname (the default without --subdomain or --domain)")
	addAllowIPFlag(fl, &f.allowIPs)
	addRestartFlag(fl, &f.restart)
	return cmd
}

// share is a tund serve command line that passed its checks: what to share
// and the tunnel for it, still without its Handler.
type share struct {
	root    fileserve.Root
	spec    client.TunnelSpec
	warning string // also one of spec.Notes, which log lines leave out
}

// checkServe runs the checks of tund serve that come before connecting, in
// this order: the share-root policy, --upload with a single file, password or
// single sign-on, and the hostname flags.
func checkServe(path string, f serveFlags) (share, error) {
	root, err := fileserve.CheckRoot(path, f.upload)
	if err != nil {
		// Only for a path that isn't there: a folder named 2024 that is
		// refused for another reason is no port.
		if _, serr := os.Stat(path); errors.Is(serr, fs.ErrNotExist) && portLike(path) {
			err = fmt.Errorf("%w; to expose a local server on port %s, use: tund http %s", err, path, path)
		}
		return share{}, err
	}
	if f.upload && !root.Dir {
		return share{}, fmt.Errorf("--upload needs a folder: visitors can only download a single shared file (%s)\n\nTo collect files, share a folder:\n  tund serve ./inbox --upload --password %s", client.Printable(root.Name), client.RandomPassword())
	}
	if f.password == "" && f.oidc == "" {
		// The explanations get lines of their own: a trailing # comment
		// is passed on as arguments by cmd.exe, and by zsh unless
		// interactive_comments is set (it isn't by default).
		p := shellQuote(path)
		return share{}, fmt.Errorf("tund serve needs --password or --oidc: shared files are never public\n\nShare it with this random password, or your own:\n  tund serve %s --password %s\nOr let visitors sign in with an OIDC provider from the dashboard:\n  tund serve %s --oidc <provider> --oidc-allow @example.com", p, client.RandomPassword(), p)
	}
	auth, err := client.BuildAuth(f.password, f.oidc, f.allow)
	if err != nil {
		return share{}, err
	}
	for i, a := range auth.Allow {
		if a == "'*'" || a == `"*"` {
			auth.Allow[i] = "*" // cmd.exe passes the quotes on
		}
	}
	anyone := false
	switch {
	case auth.Mode == protocol.AuthPassword && utf8.RuneCountInString(auth.Password) < 8:
		return share{}, errors.New("use a password with at least 8 characters (or omit --password to get a suggested one)")
	case auth.Mode == protocol.AuthOIDC && len(auth.Allow) == 0:
		return share{}, fmt.Errorf("--oidc needs --oidc-allow: who may open the share, e.g. --oidc-allow @example.com (emails, @domains or group:<name>), or --oidc-allow '*' for anyone who can sign in to %s", f.oidc)
	case slices.Contains(auth.Allow, "*"):
		if len(auth.Allow) > 1 {
			return share{}, errors.New("--oidc-allow '*' lets in anyone who can sign in: use it on its own, not with other entries")
		}
		auth.Allow, anyone = nil, true // an empty allow list admits everyone
	}
	sub, domain := strings.ToLower(strings.TrimSpace(f.subdomain)), strings.ToLower(strings.TrimSpace(f.domain))
	if sub != "" && domain != "" {
		return share{}, errors.New("use either --subdomain or --domain, not both")
	}
	if f.random && (sub != "" || domain != "") {
		return share{}, errors.New("--random cannot be combined with --subdomain or --domain")
	}
	if f.pin && sub == "" && domain == "" {
		// Pinning a random hostname would also make it the account's
		// default one, which every later tund http gets.
		return share{}, errors.New("--pin needs --subdomain or --domain: a share already keeps its random hostname for its path")
	}
	spec := client.TunnelSpec{
		Name:      cmp.Or(f.name, serveName(root.Name)),
		LocalAddr: "http://file-share", // only a label for the server: the path stays on this machine
		Display:   client.Printable(root.Shown),
		Subdomain: sub,
		Hostname:  domain,
		Auth:      auth,
		Pin:       f.pin,
		// A share never lands on the account's default static hostname.
		Random:   f.random || (sub == "" && domain == ""),
		AllowIPs: f.allowIPs,

		RestartOnExpiry: f.restart,
	}
	switch {
	case !root.Dir:
		note := "visitors get a download page for " + client.Printable(root.Name)
		if fi, err := os.Stat(root.Path); err == nil {
			note += " (" + client.HumanBytes(fi.Size()) + ")"
		}
		spec.Notes = []string{note}
	case f.upload:
		spec.Badge = "upload"
		spec.Notes = []string{"visitors can browse, download and upload files · uploads never replace existing files"}
	default:
		spec.Notes = []string{"read-only: visitors can browse and download files"}
	}
	if root.Dir {
		spec.Notes = append(spec.Notes, "hidden from visitors: dotfiles and secret files (.env, keys, credentials)")
	}
	sh := share{root: root, spec: spec}
	if anyone {
		sh.warning = "anyone who can sign in to " + f.oidc + " can open this share"
		sh.spec.Notes = append(sh.spec.Notes, sh.warning)
	}
	return sh, nil
}

// portLike reports a path that reads like a port number: digits only, 1 to
// 65535.
func portLike(path string) bool {
	n, err := strconv.Atoi(path)
	return err == nil && n > 0 && n <= 65535 && strings.Trim(path, "0123456789") == ""
}

// openShare opens the file server of a share (a variable for tests).
var openShare = fileserve.New

// serveShare serves sh until ctx ends or the tunnel stops for good, and
// closes the share either way: uploads still in progress stop and leave no
// temp files behind.
func serveShare(ctx context.Context, g *globals, f serveFlags, sh share, display *client.Display) error {
	srv, err := openShare(fileserve.Options{
		Path:     sh.root.Path,
		Upload:   f.upload,
		OnUpload: func(u fileserve.Upload) { display.Upload(uploadEvent(u)) },
	})
	if err != nil {
		return err
	}
	closeShare := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Close(ctx) // past the deadline it still removes what uploads left behind
	}
	defer closeShare()
	// Ctrl+C stops the uploads in progress before the tunnel goes down
	// under them, so they end as stopped, not as broken off: the tunnel
	// runs on until the share is closed.
	run, stopRun := context.WithCancel(context.WithoutCancel(ctx))
	defer stopRun()
	defer context.AfterFunc(ctx, func() {
		closeShare()
		stopRun()
	})()
	if sh.warning != "" && !display.Pretty() {
		display.Warn(sh.warning)
	}
	sh.spec.Handler = srv
	return runSpecWith(run, g, sh.spec, display)
}

// uploadEvent turns an upload to the share into a line for the terminal.
func uploadEvent(u fileserve.Upload) client.UploadEvent {
	ev := client.UploadEvent{Path: u.Path, Requested: u.Requested, Size: u.Size, Renamed: u.Renamed, Taken: u.Taken, User: u.User, Remote: u.Remote}
	if u.Err != nil {
		ev.Error = u.Err.Error() // short phrases like "the disk is full"
	}
	return ev
}

// serveName is the default tunnel name: "serve-" plus the letters and digits
// of the shared file or folder's name, lowercased and joined by dashes, at
// most 32 characters; "serve" when nothing is left.
func serveName(base string) string {
	words := strings.FieldsFunc(strings.ToLower(base), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	if len(words) == 0 {
		return "serve"
	}
	name := "serve-" + strings.Join(words, "-")
	if len(name) > 32 {
		name = strings.TrimRight(name[:32], "-")
	}
	return name
}

// shellQuote quotes a path for a command line to copy when it needs it.
func shellQuote(s string) string {
	return quoteArg(s, runtime.GOOS == "windows")
}

// quoteArg quotes s for POSIX shells: in single quotes. For Windows it uses
// double quotes, which cmd.exe and PowerShell both read, unless s holds what
// PowerShell expands inside them ($ and `): then PowerShell's single quotes,
// as no quoting suits both shells.
func quoteArg(s string, windows bool) string {
	if strings.HasPrefix(s, "-") {
		s = "./" + s // not a flag
	}
	plain := func(r rune) bool {
		return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || strings.ContainsRune("%+-./:_", r) ||
			r == ',' && !windows || r == '\\' && windows // PowerShell splits a,b into two arguments
	}
	switch {
	case s != "" && !strings.ContainsFunc(s, func(r rune) bool { return !plain(r) }):
		return s
	case !windows:
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	case strings.ContainsAny(s, "$`\""):
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return `"` + s + `"`
}
