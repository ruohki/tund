package secretfile

// The rule tables. Entries are Fold keys (lower case); see group for the
// pattern syntax. Names are also matched with backup markers removed (see
// variants), so "credentials.json.bak", "id_rsa copy 2", "secrets 2.yml" and
// "Copy of token.json" count as well.

var (
	dirRules   = newRules(dirGroups)
	fileRules  = newRules(fileGroups)
	plantRules = newRules(plantGroups)
)

// dirGroups hide directories, and with them everything inside. They also
// apply to symlinks to directories.
var dirGroups = []group{
	{rule: "version control", patterns: []string{".git", ".svn", ".hg", ".bzr", "_darcs"}},
	{rule: "key folder", patterns: []string{".ssh", ".gnupg", ".password-store", "private-keys-v1.d", "keychains", "keyrings"}},
	{rule: "cloud credentials", patterns: []string{
		".aws", ".azure", ".gcloud", ".oci", ".kube", ".docker", ".cloudflared", ".wrangler", ".fly", ".supabase",
		".pulumi", ".doppler", ".chef", ".terraform", ".terraform.d", ".vagrant.d",
	}},
	{rule: "package manager credentials", patterns: []string{".m2", ".gradle", ".nuget", ".cargo", ".gem", ".bundle"}},
	{rule: "app data", patterns: []string{".config", ".local", ".mozilla", ".thunderbird", ".vnc", ".electrum", ".bitcoin", ".ethereum"}},
	{rule: "secret folder", patterns: []string{"secrets", ".secrets"}},
	// Vaults kept as directory packages (Keychain, 1Password).
	{rule: "password store", patterns: []string{"*.keychain", "*.opvault", "*.agilekeychain"}},
}

// fileGroups hide everything that isn't a directory.
var fileGroups = []group{
	// Exact names.
	{rule: "ssh file", patterns: []string{"identity", "authorized_keys", "authorized_keys2", "known_hosts", "known_hosts.old"}},
	{rule: "env file", patterns: []string{".env", ".envrc", ".dev.vars"}},
	{rule: "app secrets", patterns: []string{
		".secrets", "secret", "secrets", "credentials", ".credentials", "master.key", "local.settings.json",
		"local_settings.py", "settings.local.php", ".htpasswd", "htpasswd", ".htdigest", ".vault-token",
		".erlang.cookie", ".xauthority", ".iceauthority",
	}},
	{rule: "version control credentials", patterns: []string{".git-credentials", ".gitconfig", ".hgrc", ".cvspass"}},
	{rule: "package manager credentials", patterns: []string{
		".npmrc", ".yarnrc", ".yarnrc.yml", ".pypirc", ".netrc", "_netrc", ".authinfo", ".authinfo.gpg",
		"credentials.toml", "pub-credentials.json", "settings-security.xml",
	}},
	{rule: "cloud credentials", patterns: []string{
		".boto", ".s3cfg", ".passwd-s3fs", "passwd-s3fs", "rclone.conf", "application_default_credentials.json",
		"credentials.db", "access_tokens.db", "accesstokens.json", "msal_token_cache.json", "msal_token_cache.bin",
		"service_principal_entries.json", "credentials.csv", "rootkey.csv", ".terraformrc", "terraform.rc",
		"credentials.tfrc.json", ".dockercfg", "kubeconfig", "k3s.yaml", "talosconfig", "tailscaled.state",
		"identity.secret", "ipsec.secrets",
	}},
	{rule: "oauth token", patterns: []string{"credentials.json", "token.json", "token.pickle"}},
	{rule: "database credentials", patterns: []string{
		".pgpass", "pgpass.conf", ".my.cnf", ".mylogin.cnf", "debian.cnf", ".dbshell", "cwallet.sso",
	}},
	{rule: "signing config", patterns: []string{"key.properties", "keystore.properties"}},
	{rule: "browser data", patterns: []string{
		"cookies", "cookies-journal", "login data", "login data-journal", "login data for account", "web data",
		"local state", "logins.json", "logins-backup.json", "key3.db", "key4.db", "signons.sqlite", "cookies.sqlite",
		"cookies.sqlite-wal", "cookies.sqlite-shm",
	}},
	{rule: "system passwords", patterns: []string{"shadow", "shadow-", "gshadow", "gshadow-", "master.passwd", "ntds.dit"}},
	{rule: "shell history", patterns: []string{
		".bash_history", ".zsh_history", ".zhistory", ".sh_history", ".history", "fish_history", ".python_history",
		".node_repl_history", ".psql_history", ".mysql_history", ".sqlite_history", ".rediscli_history",
		".irb_history", ".pry_history", ".lesshst", ".viminfo", ".rhistory", "consolehost_history.txt",
	}},
	{rule: "mail credentials", patterns: []string{".msmtprc", ".fetchmailrc", ".muttrc"}},
	{rule: "saved logins", patterns: []string{
		"sftp.json", "sftp-config.json", ".ftpconfig", "sitemanager.xml", "recentservers.xml", "filezilla.xml",
		"winscp.ini", "datasources.local.xml", ".smbcredentials", "smbpasswd",
	}},
	{rule: "wallet", patterns: []string{"wallet.dat"}},
	{rule: "tunnel config", patterns: []string{"tund.yml", "tund.yaml", "ngrok.yml", "frpc.ini", "frpc.toml", "frpc.yaml"}},

	// Patterns.
	{rule: "env file", patterns: []string{".env.*", "*.env"},
		except: []string{"*.example", "*.sample", "*.template", "*.dist", "*.defaults", "*.schema"}},
	// Any suffix (_sk, _work, .old), but not public keys or certificates.
	{rule: "ssh key", patterns: []string{"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*", "id_xmss*"},
		except: []string{"*.pub"}},
	{rule: "ssh key", patterns: []string{"ssh_host_*_key"}},
	{rule: "private key", patterns: []string{"*.ppk", "*.p8", "*.pk8", "*.pepk", "*.pvk", "*.snk"}},
	{rule: "key store", patterns: []string{"*.p12", "*.pfx", "*.jks", "*.jceks", "*.bks", "*.keystore"}},
	{rule: "keychain", patterns: []string{"*.keychain", "*.keychain-db"}},
	{rule: "password store", patterns: []string{"*.kdbx", "*.kdb", "*.keyx", "*.psafe3", "*.1pux", "*.1pif", "*.kwallet"}},
	{rule: "password store", patterns: []string{"bitwarden_export*", "*password*.csv"}},
	{rule: "kerberos ticket", patterns: []string{"*.keytab", "krb5cc*"}},
	{rule: "terraform state", patterns: []string{"*.tfstate", "*.tfstate.backup", "*.tfvars", "*.tfvars.json"}},
	{rule: "vpn profile", patterns: []string{"*.ovpn", "*.nmconnection"}},
	{rule: "browser data", patterns: []string{"*.binarycookies"}},
	{rule: "cloud credentials", patterns: []string{
		"kubeconfig*", "*.kubeconfig", "client_secret*.json", "service-account*.json", "service_account*.json",
		"*firebase-adminsdk*.json", "*accesskeys.csv",
	}},
	{rule: "wallet", patterns: []string{"utc--*"}},
	{rule: "pgp keyring", patterns: []string{"secring.*"}},
	{rule: "ansible vault password", patterns: []string{"*vault_pass*", "*vault-pass*", ".vault_password*"}},
	{rule: "wordpress config", patterns: []string{"wp-config*"}, except: []string{"wp-config-sample.php"}},
	// .*_history and extension-less *_history are the same pattern.
	{rule: "shell history", patterns: []string{"*_history", "*_history.txt"}},
	// Config-style names: secrets.yml, db-credentials.json, app.secret.
	{rule: "secrets file", patterns: withConfigExts("secret", "secrets", "credentials", "*-credentials", "*_credentials")},
	{rule: "secrets file", patterns: []string{"*.secret", "*.secrets"}},
}

// configExts are the formats of config-style secret files.
var configExts = []string{"json", "yml", "yaml", "toml", "xml", "ini", "conf", "cfg", "env", "properties", "txt", "csv", "key"}

func withConfigExts(stems ...string) []string {
	var ps []string
	for _, s := range stems {
		for _, e := range configExts {
			ps = append(ps, s+"."+e)
		}
	}
	return ps
}

// plantGroups are names visitors can't upload because other programs pick
// them up or parse them on their own: Explorer and desktop shells, build
// tools, Python, Docker Compose and JavaScript tooling.
var plantGroups = []group{
	{rule: "windows shell file", patterns: []string{
		"desktop.ini", "autorun.inf", "*.lnk", "*.url", "*.scf", "*.library-ms", "*.searchconnector-ms",
		"*.settingcontent-ms",
	}},
	{rule: "desktop launcher", patterns: []string{"*.desktop"}},
	{rule: "build file", patterns: []string{
		"makefile", "gnumakefile", "directory.build.props", "directory.build.targets", "directory.build.rsp",
		"directory.packages.props",
	}},
	{rule: "python startup file", patterns: []string{"conftest.py", "sitecustomize.py", "usercustomize.py", "*.pth"}},
	{rule: "compose override", patterns: []string{
		"docker-compose.override.yml", "docker-compose.override.yaml", "compose.override.yml", "compose.override.yaml",
	}},
	{rule: "tool config", patterns: []string{
		"*.config.js", "*.config.cjs", "*.config.mjs", "*.config.ts", "*.config.mts", "*.config.cts",
	}},
}
