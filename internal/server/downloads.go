package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

var downloadName = regexp.MustCompile(`^tund-(linux|darwin|windows)-(amd64|arm64)(\.exe)?$`)

const installSh = `#!/bin/sh
# tund client installer for Linux and macOS.
#   curl -fsSL {{.Server}}/_tund/install.sh | sh
# Set TUND_INSTALL_DIR to choose the target directory.
set -eu

SERVER="{{.Server}}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) echo "tund: unsupported OS '$os' (on Windows use install.ps1)" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "tund: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

base="{{.Downloads}}"
name="tund-$os-$arch"
tmp=$(mktemp)
sums=$(mktemp)
trap 'rm -f "$tmp" "$sums"' EXIT
fetch() { if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"; else wget -qO "$2" "$1"; fi; }
echo "Downloading tund for $os/$arch ..."
fetch "$base/$name" "$tmp"
# Verify the checksum published with the release when a SHA-256 tool exists.
if fetch "$base/checksums.txt" "$sums" 2>/dev/null; then
  want=$(grep " $name\$" "$sums" | cut -d' ' -f1)
  if command -v sha256sum >/dev/null 2>&1; then have=$(sha256sum "$tmp" | cut -d' ' -f1)
  elif command -v shasum >/dev/null 2>&1; then have=$(shasum -a 256 "$tmp" | cut -d' ' -f1)
  else have="$want"; fi
  if [ -n "$want" ] && [ "$want" != "$have" ]; then
    echo "tund: checksum mismatch for $name (expected $want, got $have)" >&2
    exit 1
  fi
fi
chmod 755 "$tmp"

dest="${TUND_INSTALL_DIR:-}"
if [ -z "$dest" ]; then
  if [ -w /usr/local/bin ]; then
    dest=/usr/local/bin
  elif command -v sudo >/dev/null 2>&1 && [ -e /dev/tty ]; then
    echo "Installing to /usr/local/bin (sudo may ask for your password) ..."
    sudo mkdir -p /usr/local/bin
    sudo install -m 755 "$tmp" /usr/local/bin/tund
    dest=/usr/local/bin
  else
    dest="$HOME/.local/bin"
  fi
fi
if [ ! -x "$dest/tund" ] || ! cmp -s "$tmp" "$dest/tund"; then
  mkdir -p "$dest"
  install -m 755 "$tmp" "$dest/tund"
fi

"$dest/tund" config set-server "$SERVER" >/dev/null
echo "Installed $("$dest/tund" version) to $dest/tund"
case ":$PATH:" in
  *":$dest:"*) ;;
  *) echo "Note: $dest is not in your PATH. Add it, e.g.: export PATH=\"$dest:\$PATH\"" ;;
esac
echo
echo "Next steps:"
echo "  tund login        # opens your browser to sign in"
echo "  tund http 3000"
`

const installPs1 = `# tund client installer for Windows.
#   irm {{.Server}}/_tund/install.ps1 | iex
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$server = '{{.Server}}'
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$dir = Join-Path $env:LOCALAPPDATA 'tund'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$exe = Join-Path $dir 'tund.exe'
Write-Host "Downloading tund for windows/$arch ..."
$name = "tund-windows-$arch.exe"
Invoke-WebRequest -UseBasicParsing -Uri "{{.Downloads}}/$name" -OutFile $exe
try {
  $sums = (Invoke-WebRequest -UseBasicParsing -Uri "{{.Downloads}}/checksums.txt").Content
  $want = ($sums -split "\r?\n" | Where-Object { $_ -match " $([regex]::Escape($name))$" }) -replace ' .*', ''
  if ($want -and ((Get-FileHash $exe -Algorithm SHA256).Hash.ToLower() -ne $want.Trim().ToLower())) {
    Remove-Item $exe; throw "checksum mismatch for $name"
  }
} catch [System.Net.WebException] { }
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $dir)) {
  [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $dir), 'User')
  $env:Path = "$env:Path;$dir"
  Write-Host "Added $dir to your PATH (open a new terminal to pick it up)."
}
& $exe config set-server $server | Out-Null
Write-Host "Installed $(& $exe version) to $exe"
Write-Host ""
Write-Host "Next steps:"
Write-Host "  tund login        # opens your browser to sign in"
Write-Host "  tund http 3000"
`

var installTemplates = map[string]*template.Template{
	"/_tund/install.sh":  template.Must(template.New("sh").Parse(installSh)),
	"/_tund/install.ps1": template.Must(template.New("ps1").Parse(installPs1)),
}

func (s *Server) serveInstall(w http.ResponseWriter, r *http.Request) {
	t := installTemplates[r.URL.Path]
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	body := strings.Builder{}
	t.Execute(&body, map[string]string{"Server": s.cfg.DashboardURL(), "Downloads": s.cfg.DownloadBaseURL})
	out := body.String()
	if strings.HasSuffix(r.URL.Path, ".ps1") {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	fmt.Fprint(w, out)
}

// serveDownload redirects to the published release asset (GitHub Releases by
// default). A file in TUND_DOWNLOADS_DIR, if configured, is served directly.
func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/_tund/downloads/")
	if name == "" {
		s.listDownloads(w, r)
		return
	}
	if !downloadName.MatchString(name) && name != "checksums.txt" {
		http.NotFound(w, r)
		return
	}
	if s.cfg.DownloadsDir != "" {
		path := filepath.Join(s.cfg.DownloadsDir, name)
		if _, err := os.Stat(path); err == nil {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
			http.ServeFile(w, r, path)
			return
		}
	}
	http.Redirect(w, r, s.cfg.DownloadBaseURL+"/"+name, http.StatusFound)
}

func (s *Server) listDownloads(w http.ResponseWriter, r *http.Request) {
	var files []map[string]string
	for _, os := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := "tund-" + os + "-" + arch
			if os == "windows" {
				name += ".exe"
			}
			files = append(files, map[string]string{"name": name, "url": s.cfg.DownloadBaseURL + "/" + name})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "checksums": s.cfg.DownloadBaseURL + "/checksums.txt"})
}
