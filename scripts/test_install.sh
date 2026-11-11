#!/usr/bin/env bash
# Deterministic tests for the POSIX shell installer. No network or host installs.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
installer="$repo/scripts/install.sh"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/pf-install-test.XXXXXXXX")"
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/mockbin" "$tmp/releases"

cat > "$tmp/mockbin/uname" <<'EOF'
#!/bin/sh
case "$1" in
  -s) printf '%s\n' "${TEST_UNAME_S:-Linux}" ;;
  -m) printf '%s\n' "${TEST_UNAME_M:-x86_64}" ;;
  *) exit 2 ;;
esac
EOF

cat > "$tmp/mockbin/curl" <<'EOF'
#!/bin/sh
out=''
url=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) out=$2; shift 2 ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
[ -n "$out" ] && [ -n "$url" ] || exit 2
case "$url" in
  https://api.github.com/repos/nskaggs/perfuncted/releases/latest)
    printf '{"tag_name":"%s"}\n' "${TEST_LATEST_VERSION:-v1.3.1}" > "$out"
    ;;
  https://github.com/nskaggs/perfuncted/releases/download/*/checksums.txt)
    [ "${TEST_CURL_FAIL:-}" != checksums ] || exit 22
    cp "$TEST_RELEASE_ROOT/checksums.txt" "$out"
    ;;
  https://github.com/nskaggs/perfuncted/releases/download/*/perfuncted_*.tar.gz)
    [ "${TEST_CURL_FAIL:-}" != archive ] || exit 22
    cp "$TEST_RELEASE_ROOT/${url##*/}" "$out"
    ;;
  *) printf 'unexpected installer URL: %s\n' "$url" >&2; exit 2 ;;
esac
EOF

chmod 755 "$tmp/mockbin/uname" "$tmp/mockbin/curl"

make_fixture() {
  local arch fixture_bin archive
  mkdir -p "$tmp/archive"
  : > "$tmp/releases/checksums.txt"
  for arch in amd64 arm64; do
    fixture_bin="$tmp/archive/pf"
    cat > "$fixture_bin" <<EOF
#!/bin/sh
# fixture architecture: $arch
[ "\$1" = version ] || exit 1
printf 'pf %s\\n' "${TEST_BINARY_VERSION:-1.3.1}"
EOF
    chmod 755 "$fixture_bin"
    archive="perfuncted_1.3.1_linux_${arch}.tar.gz"
    tar -C "$tmp/archive" -czf "$tmp/releases/$archive" pf
    (cd "$tmp/releases" && sha256sum "$archive") >> "$tmp/releases/checksums.txt"
  done
}

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
check_version() {
  local dir=$1
  [ -x "$dir/pf" ] || fail "missing executable $dir/pf"
  [ "$("$dir/pf" version)" = 'pf 1.3.1' ] || fail "incorrect version"
}
run_installer() {
  env PATH="$tmp/mockbin:$PATH" TEST_RELEASE_ROOT="$tmp/releases" \
    PF_INSTALL_DIR="$tmp/bin" "$@" sh "$installer"
}
assert_failure() {
  local description=$1
  shift
  if "$@" >"$tmp/stdout" 2>"$tmp/stderr"; then
    fail "$description unexpectedly succeeded"
  fi
  printf 'PASS: %s\n' "$description"
}

make_fixture

run_installer env PF_VERSION=v1.3.1 >"$tmp/stdout"
check_version "$tmp/bin"
grep -q '# fixture architecture: amd64' "$tmp/bin/pf" || fail "amd64 selection"
printf 'PASS: pinned amd64 installation\n'

run_installer env PF_VERSION=1.3.1 >"$tmp/stdout"
check_version "$tmp/bin"
printf 'PASS: numeric version normalization and reinstall\n'

run_installer env -u PF_VERSION >"$tmp/stdout"
check_version "$tmp/bin"
printf 'PASS: latest stable resolution\n'

run_installer env PF_VERSION=v1.3.1 TEST_UNAME_M=aarch64 >"$tmp/stdout"
grep -q '# fixture architecture: arm64' "$tmp/bin/pf" || fail "arm64 selection"
printf 'PASS: arm64 selection\n'

cp "$tmp/bin/pf" "$tmp/previous"
assert_failure 'unsupported OS' run_installer env PF_VERSION=v1.3.1 TEST_UNAME_S=Darwin
assert_failure 'unsupported architecture' run_installer env PF_VERSION=v1.3.1 TEST_UNAME_M=riscv64
assert_failure 'invalid version' run_installer env PF_VERSION='v1.3.1/../../bad'
assert_failure 'invalid GitHub latest response' run_installer env -u PF_VERSION TEST_LATEST_VERSION=invalid
assert_failure 'failed checksum download' run_installer env PF_VERSION=v1.3.1 TEST_CURL_FAIL=checksums
assert_failure 'failed archive download' run_installer env PF_VERSION=v1.3.1 TEST_CURL_FAIL=archive

printf '\ninvalid-data' >> "$tmp/releases/perfuncted_1.3.1_linux_amd64.tar.gz"
assert_failure 'corrupt archive checksum' run_installer env PF_VERSION=v1.3.1
cmp -s "$tmp/bin/pf" "$tmp/previous" || fail "checksum failure changed installed binary"
printf 'PASS: failed install preserves previous executable\n'

unset TEST_BINARY_VERSION
TEST_BINARY_VERSION=2.0.0 make_fixture
assert_failure 'archive version mismatch' run_installer env PF_VERSION=v1.3.1
cmp -s "$tmp/bin/pf" "$tmp/previous" || fail "version mismatch replaced executable"
printf 'PASS: mismatched binary preserves previous executable\n'

make_fixture
mkdir -p "$tmp/home with spaces"
env PATH="$tmp/mockbin:$PATH" TEST_RELEASE_ROOT="$tmp/releases" \
  HOME="$tmp/home with spaces" PF_VERSION=v1.3.1 sh "$installer" >"$tmp/stdout"
check_version "$tmp/home with spaces/.local/bin"
printf 'PASS: default user-local installation with spaces in HOME\n'

assert_failure 'relative installation path' run_installer env PF_VERSION=v1.3.1 PF_INSTALL_DIR=relative
mkdir -p "$tmp/is-a-dir/pf"
assert_failure 'existing directory named pf' run_installer env PF_VERSION=v1.3.1 PF_INSTALL_DIR="$tmp/is-a-dir"

[ "$(sh "$installer" --help | grep -c 'PF_VERSION')" -eq 1 ] || fail "help output"
printf 'PASS: installer help\n'

if [ "${PF_TEST_REAL_RELEASE:-0}" = 1 ]; then
  live="$tmp/live"
  PF_VERSION=v1.3.1 PF_INSTALL_DIR="$live" sh "$installer"
  [ "$("$live/pf" version | head -n1)" = 'pf 1.3.1' ] || fail "published binary version"
  printf 'PASS: genuine v1.3.1 published release installation\n'
fi

printf 'Installer tests passed.\n'
