#!/bin/sh
# Install the published Perfuncted Linux CLI without a compiler or sudo.
set -eu

usage() {
  cat <<'EOF'
Install pf from official GitHub Releases.

Usage: sh install.sh [--help]

Environment:
  PF_VERSION       Release to install (e.g. v1.3.1); defaults to latest stable
  PF_INSTALL_DIR   Destination directory; defaults to $HOME/.local/bin

Rerun the installer to upgrade. It does not modify shell profiles or use sudo.
EOF
}

die() {
  printf 'pf installer: %s\n' "$*" >&2
  exit 1
}

case ${1:-} in
  -h|--help) usage; exit 0 ;;
  '') ;;
  *) die "unknown argument: $1" ;;
esac

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
