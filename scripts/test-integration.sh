#!/usr/bin/env bash
# scripts/test-integration.sh — run perfuncted integration tests
#
# Usage:
#   bash scripts/test-integration.sh x11       # Run the suite against X11
#   bash scripts/test-integration.sh wayland   # Run the suite against Wayland (default)
#   bash scripts/test-integration.sh nested    # Run the suite against nested Wayland

set -euo pipefail
cd "$(dirname "$0")/.."

MODE="wayland"

# Parse simple args.
for arg in "$@"; do
    case "$arg" in
        x11|wayland|nested)
            MODE="$arg"
            ;;
    esac
done

case "$MODE" in
    x11)
        echo "▶ running integration tests against X11"
        PF_TEST_DISPLAY_SERVER=x11 go test -tags=integration ./integration -count=1
        ;;
    wayland)
        echo "▶ running integration tests against Wayland"
        PF_TEST_DISPLAY_SERVER=wayland go test -tags=integration ./integration -count=1
        ;;
    nested)
        echo "▶ running integration tests against nested Wayland"
        PF_TEST_DISPLAY_SERVER=nested go test -tags=integration ./integration -count=1
        ;;
    *)
        echo "Usage: $0 {x11|wayland|nested}" >&2
        exit 2
        ;;
esac
