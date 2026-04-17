#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

mode="${1:-headless}"

case "$mode" in
    headless)
        bash scripts/legacy/test-wayland.sh headless
        ;;
    nested)
        bash scripts/legacy/test-wayland.sh nested
        ;;
    desktop)
        bash scripts/legacy/test-desktop.sh
        ;;
    *)
        echo "Usage: $0 {headless|nested|desktop}" >&2
        exit 2
        ;;
esac
