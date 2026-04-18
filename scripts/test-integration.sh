#!/usr/bin/env bash
# scripts/test-integration.sh — run perfuncted integration tests
#
# Usage:
#   bash scripts/test-integration.sh headless   # Run in a new isolated headless session (default)
#   bash scripts/test-integration.sh nested     # Run against an existing nested session
#   bash scripts/test-integration.sh desktop    # Run against the host desktop
#   bash scripts/test-integration.sh --app kwrite headless # Filter by app

set -euo pipefail
cd "$(dirname "$0")/.."

MODE="headless"
APP=""

# Parse simple args
for arg in "$@"; do
    case "$arg" in
        headless|nested|desktop)
            MODE="$arg"
            ;;
        --app)
            # handle next arg in next loop or just simple check
            ;;
    esac
done

# If --app was used, we might need a more complex parser, but let's keep it simple for now
# and just pass all args to the go program.

EXTRA_FLAGS=""
if [[ "$*" == *"--app"* ]]; then
    # flags are already in $@, we'll just pass them through
    true
fi

case "$MODE" in
    headless)
        echo "▶ running integration tests in HEADLESS mode"
        go run ./cmd/integration --headless "$@"
        ;;
    nested)
        echo "▶ running integration tests in NESTED mode"
        # Ensure a nested session exists
        if ! ls /tmp/perfuncted-xdg-* >/dev/null 2>&1; then
            echo "error: no nested session found. Run 'just nested' first." >&2
            exit 1
        fi
        go run ./cmd/integration --nested "$@"
        ;;
    desktop)
        echo "▶ running integration tests on DESKTOP (host)"
        go run ./cmd/integration "$@"
        ;;
    *)
        echo "Usage: $0 {headless|nested|desktop} [--app NAME]" >&2
        exit 2
        ;;
esac
