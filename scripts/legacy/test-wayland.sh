#!/usr/bin/env bash
# Legacy entrypoint kept for existing local workflows.

set -euo pipefail
cd "$(dirname "$0")/../.."

MODE="${1:-wayland}"
case "$MODE" in
    headless|wayland)
        PF_TEST_DISPLAY_SERVER=wayland go test -tags=integration ./integration -count=1
        ;;
    nested)
        PF_TEST_DISPLAY_SERVER=nested go test -tags=integration ./integration -count=1
        ;;
    *)
        echo "Usage: $0 {headless|wayland|nested}" >&2
        exit 2
        ;;
esac
