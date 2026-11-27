#!/bin/sh
# Install the published Perfuncted Linux CLI without a compiler or sudo.
set -eu

usage() {
  cat <<'EOF'
Install pf from official GitHub Releases.

Usage: sh install.sh [--help]
       sh install.sh --authorize-kwin [ABSOLUTE_PF_PATH]
       sh install.sh --remove-kwin

Environment:
  PF_VERSION       Release to install (e.g. v1.3.1); defaults to latest stable
  PF_INSTALL_DIR   Destination directory; defaults to $HOME/.local/bin
  PF_KWIN_CAPTURE  yes/no/ask (default ask on KDE Wayland; unattended no)

Rerun the installer to upgrade. It does not modify shell profiles or use sudo.
EOF
}

die() {
  printf 'pf installer: %s\n' "$*" >&2
  exit 1
}


register_kwin() (
[ "$#" -le 1 ] || { usage >&2; exit 2; }
[ -n "${HOME:-}" ] || { echo 'HOME required' >&2; exit 1; }
dir="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
entry="$dir/io.github.nskaggs.perfuncted.desktop"
[ ! -L "$entry" ] || die "refusing symlink desktop entry"
case "$dir" in /*) ;; *) echo 'XDG_DATA_HOME must be absolute' >&2; exit 1;; esac
command -v kbuildsycoca6 >/dev/null || { echo 'KDE application registry unavailable (kbuildsycoca6)' >&2; exit 1; }
if [ "${1:-}" = --remove ]; then
  if [ -e "$entry" ]; then
    grep -Fxq 'Name=Perfuncted' "$entry" && grep -Fxq 'X-KDE-DBUS-Restricted-Interfaces=org.kde.KWin.ScreenShot2' "$entry" ||
      { echo 'Refusing to remove unrecognized desktop entry' >&2; exit 1; }
    rm -- "$entry"
  fi
  kbuildsycoca6 --noincremental
  exit 0
fi
bin="${1:-$HOME/.local/bin/pf}"
case "$bin" in /*) ;; *) echo 'Executable path must be absolute' >&2; exit 1;; esac
[ ! -L "$bin" ] || die "KWin registration requires a stable, non-symlink pf executable; install pf at its permanent path"
bin=$(readlink -f -- "$bin")
# An exact stable path, not a shell command or desktop Exec field expansion.
case "$bin" in *[!a-zA-Z0-9_./-]*) echo 'Unsupported characters in executable path; install pf at a simple stable path' >&2; exit 1;; esac
[ -f "$bin" ] && [ -x "$bin" ] && [ "$(basename "$bin")" = pf ] || { echo 'Installed pf executable required' >&2; exit 1; }
"$bin" version >/dev/null
mkdir -p -- "$dir"
[ ! -L "$entry" ] || { echo 'Refusing symlink desktop entry' >&2; exit 1; }
if [ -e "$entry" ]; then
  grep -Fxq 'Name=Perfuncted' "$entry" && grep -Fxq 'X-KDE-DBUS-Restricted-Interfaces=org.kde.KWin.ScreenShot2' "$entry" ||
    { echo 'Refusing to overwrite unrecognized desktop entry' >&2; exit 1; }
fi
tmp=$(mktemp "$dir/.perfuncted.XXXXXXXX")
trap 'rm -f -- "$tmp"' EXIT HUP INT TERM
cat > "$tmp" <<DESKTOP
[Desktop Entry]
Type=Application
Name=Perfuncted
Comment=Owner-authorized desktop automation and screen capture
Exec=$bin
NoDisplay=true
Terminal=true
X-KDE-DBUS-Restricted-Interfaces=org.kde.KWin.ScreenShot2
DESKTOP
chmod 644 "$tmp"
mv -f -- "$tmp" "$entry"
kbuildsycoca6 --noincremental
printf '%s\n' "Registered $bin for native KWin screenshots." 'Verify with PF_SCREEN_BACKEND=kwin-shot2 pf info; remove with install.sh --remove-kwin.'

)
case ${1:-} in
  --authorize-kwin) shift; register_kwin "$@"; exit $? ;;
  --remove-kwin) [ "$#" -eq 1 ] || die "too many arguments"; register_kwin --remove; exit $? ;;
  -h|--help) usage; exit 0 ;;
  '') ;;
  *) die "unknown argument: $1" ;;
esac

case ${PF_KWIN_CAPTURE:-ask} in yes|no|ask) ;; *) die "PF_KWIN_CAPTURE must be yes, no or ask";; esac
if [ "${PF_KWIN_CAPTURE:-ask}" = yes ]; then
  command -v kbuildsycoca6 >/dev/null || die "KDE application registry unavailable (kbuildsycoca6); no installation attempted"
fi
[ "$(uname -s)" = Linux ] || die "only Linux releases are available"
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported CPU architecture: $(uname -m) (expected x86_64 or aarch64)" ;;
esac

if [ -n "${PF_INSTALL_DIR:-}" ]; then
  install_dir=$PF_INSTALL_DIR
else
  [ -n "${HOME:-}" ] || die "HOME is required unless PF_INSTALL_DIR is set"
  install_dir=$HOME/.local/bin
fi
case $install_dir in
  /*) ;;
  *) die "PF_INSTALL_DIR must be an absolute path" ;;
esac

# curl and wget both require HTTPS, including redirects.
download() {
  url=$1
  destination=$2
  if command -v curl >/dev/null 2>&1; then
    curl --proto '=https' --proto-redir '=https' --tlsv1.2 \
      --connect-timeout 15 --max-time 120 --retry 2 -fsSL \
      --output "$destination" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget --https-only --secure-protocol=TLSv1_2 --quiet \
      --output-document="$destination" "$url"
  else
    die "curl or wget is required"
  fi
}

workdir=$(mktemp -d "${TMPDIR:-/tmp}/pf-install.XXXXXXXX") || die "cannot create temporary directory"
staged=''
cleanup() {
  if [ -n "$staged" ]; then rm -f -- "$staged"; fi
  rm -rf -- "$workdir"
}
trap cleanup 0
trap 'exit 1' 1 2 3 15

version=${PF_VERSION:-}
if [ -z "$version" ]; then
  download https://api.github.com/repos/nskaggs/perfuncted/releases/latest "$workdir/release.json" \
    || die "cannot find the latest GitHub release (set PF_VERSION to install a specific release)"
  version=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$workdir/release.json" | sed -n '1p')
fi
case $version in
  v*) ;;
  *) version=v$version ;;
esac
printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' \
  || die "invalid stable release version: $version"

asset=perfuncted_${version#v}_linux_${arch}.tar.gz
release_url=https://github.com/nskaggs/perfuncted/releases/download/$version

download "$release_url/checksums.txt" "$workdir/checksums.txt" \
  || die "cannot download checksums for $version"
download "$release_url/$asset" "$workdir/$asset" \
  || die "cannot download $asset"

expected=$(awk -v file="$asset" '$2 == file {print $1}' "$workdir/checksums.txt")
printf '%s\n' "$expected" | grep -Eq '^[[:xdigit:]]{64}$' \
  || die "missing or invalid SHA-256 checksum for $asset"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$workdir/$asset" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$workdir/$asset" | awk '{print $1}')
else
  die "sha256sum or shasum is required to verify the download"
fi
[ "$actual" = "$expected" ] || die "SHA-256 checksum mismatch for $asset"

mkdir -p -- "$install_dir" || die "cannot create $install_dir"
[ -d "$install_dir" ] && [ -w "$install_dir" ] || die "cannot write to $install_dir"
[ ! -d "$install_dir/pf" ] || die "$install_dir/pf is a directory"

# Extract only the executable, not arbitrary paths from the archive. Stage on the
# destination filesystem, then atomically replace the previous installation.
staged=$(mktemp "$install_dir/.pf.XXXXXXXX") || die "cannot stage executable"
tar -xOzf "$workdir/$asset" pf > "$staged" || die "archive does not contain pf"
chmod 755 "$staged" || die "cannot make pf executable"
installed_version=$("$staged" version | sed -n '1s/^pf //p') \
  || die "downloaded pf cannot run"
[ "$installed_version" = "${version#v}" ] \
  || die "binary reports version '$installed_version', expected '${version#v}'"
mv -f -- "$staged" "$install_dir/pf" || die "cannot install pf"
staged=''

printf 'Installed pf %s to %s/pf\n' "${version#v}" "$install_dir"
case :${PATH:-}: in
  *:"$install_dir":*) ;;
  *) printf 'Add %s to PATH to run pf directly.\n' "$install_dir" ;;
esac

# On KDE, offer the one-time capability grant; unattended installs must opt in.
case ${PF_KWIN_CAPTURE:-ask} in
  yes) kwin_register=yes ;;
  no) kwin_register=no ;;
  ask)
    kwin_register=no
    case ${XDG_CURRENT_DESKTOP:-}:${XDG_SESSION_TYPE:-} in
      *KDE*:wayland)
        if ( : </dev/tty ) 2>/dev/null; then
          printf '%s' 'Authorize Perfuncted for native KWin screen capture? [y/N] ' >/dev/tty
          IFS= read -r answer </dev/tty || answer=no
          case "$answer" in y|Y|yes|YES) kwin_register=yes;; esac
        fi ;;
    esac ;;
  *) die 'PF_KWIN_CAPTURE must be yes, no or ask' ;;
esac
if [ "$kwin_register" = yes ]; then
  register_kwin "$install_dir/pf"
fi
