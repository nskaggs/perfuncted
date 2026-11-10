#!/bin/sh
set -eu
expected='go1.27.3'
actual=$("${GOROOT:-/usr/lib/sdk/golang}/bin/go" version | awk '{print $3}')
if [ "$actual" != "$expected" ]; then
  echo "Flatpak Go SDK mismatch: expected $expected, got $actual" >&2
  exit 1
fi
version=dev
commit=unknown
date=unknown
if [ -f flatpak/build-info.txt ]; then
  version=$(sed -n '1p' flatpak/build-info.txt)
  commit=$(sed -n '2p' flatpak/build-info.txt)
  date=$(sed -n '3p' flatpak/build-info.txt)
fi
case "$version" in *[!a-zA-Z0-9._+-]*|'') echo 'invalid build version' >&2; exit 1;; esac
case "$commit" in *[!a-fA-F0-9]*|'') [ "$commit" = unknown ] || { echo 'invalid commit' >&2; exit 1; };; esac
case "$date" in *[!0-9T:Z.+-]*|'') [ "$date" = unknown ] || { echo 'invalid date' >&2; exit 1; };; esac
exec "${GOROOT:-/usr/lib/sdk/golang}/bin/go" build -mod=vendor -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.version=$version -X main.commit=$commit -X main.date=$date -X main.builtBy=flatpak" \
  -o "${FLATPAK_DEST}/bin/pf" ./cmd/pf
