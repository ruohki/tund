package secretfile

import (
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func TestFold(t *testing.T) {
	for in, want := range map[string]string{
		"ID_RSA":          "id_rsa",
		"id_rſa":          "id_rsa", // long s
		".SSH":            ".ssh",
		"Login Data":      "login data",
		"STRAẞE":          "strasse",
		"ﬀile":            "ffile",
		"\u212aubeconfig": "kubeconfig",    // Kelvin sign
		"Cafe\u0301.TXT":  "caf\u00e9.txt", // NFD to NFC
		"ıd_rsa":          "ıd_rsa",        // dotless i doesn't fold, on APFS neither
		"İd_rsa":          "i\u0307d_rsa",  // full folding
		"\xff\xfeID":      "\xff\xfeid",    // invalid UTF-8 survives
		"":                "",

		// Default-ignorable code points don't count; HFS+ skips U+200C-200F,
		// U+202A-202E, U+206A-206F and U+FEFF when it looks a name up.
		"Make\u200cfile":             "makefile",
		".g\u200dit":                 ".git",
		"\u202eid_rsa\u202c":         "id_rsa",
		"\ufeffSecrets.yml":          "secrets.yml",
		"Make\u206afile\u206f":       "makefile",
		"id_\u00adrsa":               "id_rsa", // soft hyphen
		"Make\ufe0ffile\U000e0001":   "makefile",
		"\u200b\u2060":               "",
		"Cafe\u200d\u0301.TXT":       "caf\u00e9.txt", // dropped before NFC
		"\xff\u200b\xfeID":           "\xff\xfeid",
		"\xe2\x80\u200c\x8bMakefile": "makefile",             // the invalid bytes around it make another
		"Make\u00a0file":             "make\u00a0file",       // a no-break space shows
		"\U0001f469\u200d\U0001f4bb": "\U0001f469\U0001f4bb", // ZWJ emoji keep their parts
	} {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
	// The ASCII shortcut folds like x/text.
	for c := range utf8.RuneSelf {
		s := "Id_" + string(rune(c)) + "x"
		if got, want := Fold(s), cases.Fold().String(norm.NFC.String(s)); got != want {
			t.Errorf("Fold(%q) = %q, x/text folds to %q", s, got, want)
		}
	}
}

// ignorable is Unicode's Default_Ignorable_Code_Point, which
// DerivedCoreProperties.txt derives from these tables, and Fold drops every
// one of them.
func TestIgnorable(t *testing.T) {
	for r := range rune(unicode.MaxRune + 1) {
		want := unicode.In(r, unicode.Other_Default_Ignorable_Code_Point, unicode.Cf, unicode.Variation_Selector) &&
			!unicode.In(r, unicode.White_Space, unicode.Prepended_Concatenation_Mark) &&
			!(0xfff9 <= r && r <= 0xfffb || 0x13430 <= r && r <= 0x13440)
		if got := isIgnorable(r); got != want {
			t.Errorf("isIgnorable(%U) = %v, want %v", r, got, want)
		}
		if want {
			if got := Fold("Make" + string(r) + "file"); got != "makefile" {
				t.Errorf("Fold keeps %U: %q", r, got)
			}
		}
	}
}

// The spec's lists, verbatim; the names with spaces follow in TestDeniedFiles.
const (
	specDirs = `.git .svn .hg .bzr _darcs .ssh .gnupg .password-store private-keys-v1.d .aws .azure .gcloud .oci .kube
		.docker .cloudflared .wrangler .fly .supabase .pulumi .doppler .chef .terraform .terraform.d .vagrant.d
		.m2 .gradle .nuget .cargo .gem .bundle .config .local .mozilla .thunderbird .vnc .electrum .bitcoin .ethereum
		keychains keyrings secrets .secrets`
	specFiles = `identity authorized_keys authorized_keys2 known_hosts known_hosts.old
		.env .envrc .dev.vars .secrets secret secrets credentials .credentials master.key local.settings.json
		local_settings.py settings.local.php .htpasswd htpasswd .htdigest .vault-token .erlang.cookie .xauthority
		.iceauthority .git-credentials .gitconfig .hgrc .cvspass .npmrc .yarnrc .yarnrc.yml .pypirc .netrc _netrc
		.authinfo .authinfo.gpg credentials.toml pub-credentials.json settings-security.xml .boto .s3cfg .passwd-s3fs
		passwd-s3fs rclone.conf application_default_credentials.json credentials.db access_tokens.db accesstokens.json
		msal_token_cache.json msal_token_cache.bin service_principal_entries.json credentials.csv rootkey.csv
		.terraformrc terraform.rc credentials.tfrc.json .dockercfg kubeconfig k3s.yaml talosconfig tailscaled.state
		identity.secret ipsec.secrets credentials.json token.json token.pickle .pgpass pgpass.conf .my.cnf
		.mylogin.cnf debian.cnf .dbshell cwallet.sso key.properties keystore.properties cookies cookies-journal
		logins.json logins-backup.json key3.db key4.db signons.sqlite cookies.sqlite cookies.sqlite-wal cookies.sqlite-shm
		shadow shadow- gshadow gshadow- master.passwd ntds.dit .bash_history .zsh_history .zhistory .sh_history
		.history fish_history .python_history .node_repl_history .psql_history .mysql_history .sqlite_history
		.rediscli_history .irb_history .pry_history .lesshst .viminfo .rhistory consolehost_history.txt .msmtprc
		.fetchmailrc .muttrc sftp.json sftp-config.json .ftpconfig sitemanager.xml recentservers.xml filezilla.xml
		winscp.ini datasources.local.xml .smbcredentials smbpasswd wallet.dat tund.yml tund.yaml ngrok.yml frpc.ini
		frpc.toml frpc.yaml`
)

func TestDeniedDirs(t *testing.T) {
	names := append(strings.Fields(specDirs),
		"login.keychain", "1Password.opvault", "1Password.agilekeychain", // directory packages
		".SSH", ".Git", "Keychains", "SECRETS", ".ſsh", // folding
		".ssh.bak", ".aws~", ".gnupg.old", ".ssh (1)", "Secrets copy", "secrets copy 2", "Secrets - Copy", // copies
		"secrets.", ".ssh ", // Windows drops trailing dots and spaces
		".ssh 2", "Secrets(1)", "secrets - 2", "Copy of .aws", ".aws.1", // more copies
		".g\u200cit", "\u200c.ssh", ".ssh\ufeff", ".ss\u00adh", // invisible characters
	)
	for _, n := range names {
		if rule, ok := Denied(n, true); !ok || rule == "" {
			t.Errorf("Denied(%q, dir) = %q, %v; want denied", n, rule, ok)
		}
	}
	for _, n := range []string{
		"src", "Documents", "Deck.key", "ssh", "git", "config", "secret", "keys", ".github",
		".venv", ".env", // virtualenvs
		"id_rsa", "credentials",
		"src 2", "config(1)", "Copy of src", "git\u200c",
	} {
		if rule, ok := Denied(n, true); ok {
			t.Errorf("Denied(%q, dir) = %q; want allowed", n, rule)
		}
	}
}

func TestDeniedFiles(t *testing.T) {
	names := append(strings.Fields(specFiles),
		"login data", "login data-journal", "login data for account", "web data", "local state",
		"Login Data", "Login Data For Account", "Web Data", "Local State", "Cookies", "Cookies-journal",
		".Xauthority", "ConsoleHost_history.txt", "WinSCP.ini", "accessTokens.json", "dataSources.local.xml",

		// Environment files.
		".env.local", ".env.production", ".env.test", ".env.example.local", ".env.local.bak", "prod.env", "App.ENV",
		// SSH keys.
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "id_xmss", "id_rsa.old", "id_rsa_work", "id_ed25519_sk",
		"id_ecdsa-sk", "id_rsa.txt", "id_rsa.pub.bak", "ssh_host_rsa_key", "ssh_host_ed25519_key",
		// Key containers and stores.
		"server.ppk", "AuthKey_ABC123.p8", "key.pk8", "upload.pepk", "code.pvk", "strong.snk", "cert.p12", "cert.pfx",
		"release.jks", "keys.jceks", "keys.bks", "release.keystore", "login.keychain", "login.keychain-db",
		"Passwords.kdbx", "old.kdb", "unlock.keyx", "safe.psafe3", "export.1pux", "data.1pif", "kdewallet.kwallet",
		"bitwarden_export_20240101.json", "Chrome Passwords.csv", "passwords.csv",
		// Kerberos, Terraform, VPN, browsers.
		"krb5.keytab", "krb5cc_1000", "terraform.tfstate", "terraform.tfstate.backup", "prod.tfvars",
		"prod.tfvars.json", "client.ovpn", "Home WiFi.nmconnection", "Cookies.binarycookies",
		// Cloud keys and configs.
		"kubeconfig.yaml", "kubeconfig-prod", "prod.kubeconfig", "client_secret_123.apps.googleusercontent.com.json",
		"client_secret.json", "service-account.json", "service_account_key.json",
		"myapp-firebase-adminsdk-abc12-0123456789.json", "rootuser_accessKeys.csv", "accessKeys.csv",
		"UTC--2024-01-01T00-00-00.000Z--0123456789abcdef0123456789abcdef01234567", "secring.gpg", "secring.skr",
		// Ansible, WordPress, histories.
		"vault_pass.txt", ".vault_pass", "ansible-vault-pass.sh", ".vault_password", ".vault_password_file",
		"wp-config.php", "wp-config-backup.php", "wp-config.old.php", ".julia_history", "mongo_history",
		"psql_history.txt",
		// Config-style secret names.
		"secret.yml", "secrets.yaml", "secrets.json", "credentials.xml", "credentials.ini", "credentials.key",
		"secret.key", "secrets.env", "secrets.txt", "secret.conf", "secrets.cfg", "secrets.properties", "secrets.csv",
		"db-credentials.json", "aws_credentials.ini", "app.secret", "app.secrets", "SECRETS.YML",

		// Backup suffixes.
		"credentials.json.bak", "master.key.old", "wp-config.php~", "id_rsa copy", ".env (1)", "id_rsa copy 2",
		"master.key~", "master.key.bak", "master.key.backup", "master.key.orig", "master.key.save", "master.key.sav",
		"master.key.swp", "master.key.tmp", "master.key.copy", "master.key copy", "master.key copy 7",
		"master.key (3)", "master.key - Copy", "known_hosts~", "Login Data (1)", "master.key.bak.old~",
		"credentials (1).json", "secrets copy.yml", "secrets copy 2.yml", "token - Copy.json",
		"token - Copy (2).json", "credentials (1).json.bak",
		"secrets.yml.", "master.key ", ".env.",
		// Firefox, Finder, wget and Google Drive copies.
		"credentials(1).json", "token(12).json", ".env(2)", "secrets 2.yml", "credentials 3.json", "master.key 2",
		"Login Data 2", "secrets - 2.yml", "token - 10.json", "credentials.json.1", "secrets.yml.2", "master.key.1",
		"known_hosts.old.3", "Copy of credentials.json", "copy of master.key", "Copy (2) of token.json",
		"Copy of credentials (1).json.bak", "credentials(1).json.1",
		// Invisible characters.
		".en\u200cv", "id_\u200drsa", "\u200bid_rsa", "credentials\u202e.json",
		"\ufeffsecrets.yml", "master\u00ad.key", "kube\u2060config", "wp\u034f-config.php",

		// Folding.
		"ID_RSA", "id_rſa", "Id_Ed25519", "CREDENTIALS.JSON", ".ENV", "Master.Key", "KUBECONFIG", "\u212aubeconfig",
		"ſecrets.yml", "Wp-Config.php", "SSH_HOST_RSA_KEY", "TERRAFORM.TFSTATE", "TUND.YML",
	)
	for _, n := range names {
		if rule, ok := Denied(n, false); !ok || rule == "" {
			t.Errorf("Denied(%q) = %q, %v; want denied", n, rule, ok)
		}
	}
}

// The spec's must-not list and other near misses: names that look secret but
// aren't, or are encrypted, public or templates.
var notDenied = []string{
	"Deck.key", "cert.pem", "fullchain.pem", "server.crt", "ca.cer", "server.der", "dhparam.pem", "csr.pem",
	"id_card.jpg", "id_photo.png", "identity.pdf", "idea.md", "ids.csv", "id_ed25519.pub", "id_rsa-cert.pub",
	"ID_RSA.PUB", "id_ed25519 (1).pub", "google-services.json", "GoogleService-Info.plist", "firebase.json",
	"auth.json", "tokens.json", "credentials.pdf", "credentials copy.pdf", "secrets.md", "secret-santa.xlsx",
	"Secret Garden.epub", "passwords-policy.pdf", "docker-compose.yml", "database.yml", "settings.py",
	".env.example", ".env.sample", ".env.template", ".env.dist", ".env.defaults", ".env.schema",
	".env.local.example", "wp-config-sample.php", "README.md", "credentials.yml.enc", "secrets.enc.yaml",
	"history.txt", "my_history.md", "environment.yml", "report (1).pdf", "photo copy.jpg", "notes.txt~",
	"known_hosts.pub", "ssh_host_rsa_key.pub", "terraform.tfstate.d",
	"master.key.orig.bak.old~", // more than three backup suffixes
	"ıd_rsa",                   // dotless i: not id_rsa (neither on APFS); must not crash
	"", ".", "..", "~", " (1)", ".bak",
	// Numbered names that aren't copies of secrets: man pages, libraries,
	// rotated logs, other files' copies.
	"shadow.5", "gshadow.5", "credentials.7", "htpasswd.1", "smbpasswd.8", "ipsec.conf.5", "libsecret.so.1",
	"credentials.log.1", "v1.2.3", "photo 2.jpg", "Chapter 2.pdf", "credentials 2.pdf", "report(1).pdf",
	"credentials(1).pdf", "secrets 2.md", "Secret Garden 2.epub", "Copy of report.pdf", "copy of credentials.pdf",
	"id_ed25519 2.pub", "Copy of id_rsa.pub", "copy of", "(1)", "2",
	// Invisible characters in harmless names.
	"secrets\u200d.md", "\U0001f469\u200d\U0001f4bb notes.txt", "\u200b",
}

func TestNotDenied(t *testing.T) {
	for _, n := range notDenied {
		if rule, ok := Denied(n, false); ok {
			t.Errorf("Denied(%q) = %q; want allowed", n, rule)
		}
	}
	for _, n := range []string{"Deck.key", "locales", "en", "ıd_rsa", "secret", "secret garden", "Secrets 2023", "secrets - 1999"} {
		if rule, ok := Denied(n, true); ok {
			t.Errorf("Denied(%q, dir) = %q; want allowed", n, rule)
		}
	}
	// A year is part of a name, not a copy counter.
	for _, n := range []string{"token 2024.json", "secrets 2023.yml", "credentials(2023).json", "master.key 100"} {
		if rule, ok := Denied(n, false); ok {
			t.Errorf("Denied(%q) = %q; want allowed", n, rule)
		}
	}
}

func TestUploadRefused(t *testing.T) {
	for _, n := range []string{
		// Planting.
		"desktop.ini", "Desktop.ini", "AUTORUN.INF", "Report.lnk", "site.url", "explorer.scf", "x.library-ms",
		"x.searchconnector-ms", "x.settingcontent-ms", "app.desktop", "Makefile", "GNUmakefile", "makefile",
		"conftest.py", "sitecustomize.py", "usercustomize.py", "evil.pth", "Directory.Build.props",
		"Directory.Build.targets", "Directory.Build.rsp", "Directory.Packages.props", "docker-compose.override.yml",
		"docker-compose.override.yaml", "compose.override.yml", "compose.override.yaml", "vite.config.js",
		"eslint.config.cjs", "next.config.mjs", "vitest.config.ts", "x.config.mts", "x.config.cts",
		"Makefile (1)", "desktop.ini~", "Makefile 2", "Makefile(1)", "Copy of Makefile", "conftest(1).py",
		"desktop.ini.1",
		// Invisible characters: HFS+ opens these as Makefile, desktop.ini, ...
		"Make\u200cfile", "Make\u200dfile", "Make\u206afile", "\ufeffMakefile", "Makefile\u200e",
		"desktop\u202e.ini", "conftest\u200f.py", "Directory.Build\u202a.props", "vite.config\u206f.js",
		// ... and the other default-ignorable ones.
		"Make\u00adfile", "conftest\u2060.py", "Directory.Build\u034f.props", "Make\ufe0ffile",
		// Denied names.
		"id_rsa", "credentials.json", ".env", "secrets.yml", "tund.yml", "kubeconfig", "Login Data",
	} {
		if rule, ok := UploadRefused(n); !ok || rule == "" {
			t.Errorf("UploadRefused(%q) = %q, %v; want refused", n, rule, ok)
		}
	}
	for _, n := range append(slices.Clone(notDenied),
		"report.pdf", "photo.jpg", "config.js", "vite.config.json", "makefile.txt", "desktop.ini.txt", "setup.py",
		"package.json", "Dockerfile", "notes.lnk.txt", "readme.url.md",
		"Make\u200cfile.txt", "Make\u00a0file", "Make file", "notes 2.txt", "Copy of report.pdf",
	) {
		if rule, ok := UploadRefused(n); ok {
			t.Errorf("UploadRefused(%q) = %q; want allowed", n, rule)
		}
	}
}

func TestRuleNames(t *testing.T) {
	for _, c := range []struct {
		name string
		dir  bool
		rule string
	}{
		{"id_rsa", false, "ssh key"},
		{"id_ed25519.bak", false, "ssh key"},
		{".env", false, "env file"},
		{".env.local", false, "env file"},
		{".ssh", true, "key folder"},
		{".git", true, "version control"},
		{"secrets.yml", false, "secrets file"},
		{"credentials (1).json", false, "oauth token"},
		{".bash_history", false, "shell history"},
		{"credentials(1).json", false, "oauth token"},
		{"Copy of token.json", false, "oauth token"},
		{"credentials.json.1", false, "oauth token"},
		{"secrets 2.yml", false, "secrets file"},
		{".g\u200cit", true, "version control"},
	} {
		if rule, _ := Denied(c.name, c.dir); rule != c.rule {
			t.Errorf("Denied(%q, %v) rule = %q, want %q", c.name, c.dir, rule, c.rule)
		}
	}
	if rule, _ := UploadRefused("desktop.ini"); rule != "windows shell file" {
		t.Errorf("UploadRefused(desktop.ini) rule = %q", rule)
	}
	if rule, _ := UploadRefused("Make\u200cfile"); rule != "build file" {
		t.Errorf("UploadRefused(Make<U+200C>file) rule = %q", rule)
	}
}

// Every table entry must be a Fold key, or it could never match.
func TestTables(t *testing.T) {
	for _, gs := range [][]group{dirGroups, fileGroups, plantGroups} {
		for _, g := range gs {
			if g.rule == "" || len(g.patterns) == 0 {
				t.Errorf("empty group %+v", g)
			}
			for _, p := range append(slices.Clone(g.patterns), g.except...) {
				if p == "" || Fold(p) != p || strings.TrimRight(p, ". ") != p {
					t.Errorf("%s: pattern %q is not a Fold key", g.rule, p)
				}
			}
		}
	}
	for _, c := range []struct {
		rs     rules
		groups []group
		dir    bool
	}{{dirRules, dirGroups, true}, {fileRules, fileGroups, false}, {plantRules, plantGroups, false}} {
		for _, g := range c.groups {
			for _, p := range g.patterns {
				if p = strings.ReplaceAll(p, "*", "x"); g.except == nil {
					if _, ok := c.rs.find(p); !ok {
						t.Errorf("%s: %q doesn't match its own table", g.rule, p)
					}
				}
			}
		}
	}
}

func TestVariants(t *testing.T) {
	for in, want := range map[string][]string{
		"id_rsa":                   {"id_rsa"},
		"credentials (1).json.bak": {"credentials (1).json.bak", "credentials (1).json", "credentials.json"},
		"id_rsa copy 2":            {"id_rsa copy 2", "id_rsa"},
		"secrets copy 2.yml":       {"secrets copy 2.yml", "secrets.yml"},
		"token - copy (2).json":    {"token - copy (2).json", "token - copy.json", "token.json"},
		"a.bak.bak.bak.bak.bak":    {"a.bak.bak.bak.bak.bak", "a.bak.bak.bak.bak", "a.bak.bak.bak", "a.bak.bak"},
		"master.key. ":             {"master.key"},
		"x (y)":                    {"x (y)"},
		"x ()":                     {"x ()"},
		"photo2":                   {"photo2"},
		"credentials(1).json":      {"credentials(1).json", "credentials.json"},
		"secrets 2.yml":            {"secrets 2.yml", "secrets.yml"},
		"secrets 2":                {"secrets 2", "secrets"},
		"token - 2.json":           {"token - 2.json", "token.json"},
		"credentials.json.1":       {"credentials.json.1", "credentials.json"},
		"shadow.5":                 {"shadow.5"},
		"v1.2.3":                   {"v1.2.3", "v1.2"},
		"copy of credentials.json": {"copy of credentials.json", "credentials.json"},
		"copy (2) of token.json":   {"copy (2) of token.json", "token.json"},
		"copy of credentials (1).json.bak": {
			"copy of credentials (1).json.bak", "copy of credentials (1).json", "copy of credentials.json", "credentials.json",
		},
		"x(y)":    {"x(y)"},
		"(1)":     {"(1)"},
		"x -2":    {"x -2"},
		"copy of": {"copy of"},
		".bak":    {".bak"},
		"~":       {"~"},
		"...":     nil,
		"":        nil,
	} {
		if got := slices.Collect(variants(in)); !slices.Equal(got, want) {
			t.Errorf("variants(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatch(t *testing.T) {
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"*", "", true},
		{"*", "x", true},
		{"a", "a", true},
		{"a", "ab", false},
		{"a*", "a", true},
		{"*b", "ab", true},
		{"*b", "ba", false},
		{"a*b", "ab", true},
		{"a*b", "axxb", true},
		{"a*a", "a", false},
		{"*ab*ab", "ab", false},
		{"*ab*ab", "abab", true},
		{"a*b*c", "abc", true},
		{"a*b*c", "acb", false},
		{"ssh_host_*_key", "ssh_host_key", false},
		{"ssh_host_*_key", "ssh_host_rsa_key", true},
		{"*firebase-adminsdk*.json", "x-firebase-adminsdk-y.json", true},
		{"*firebase-adminsdk*.json", "firebase-adminsdk.json", true},
		{"*firebase-adminsdk*.json", "firebase-adminsdk.yml", false},
	} {
		if got := match(c.pattern, c.name); got != c.want {
			t.Errorf("match(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}
