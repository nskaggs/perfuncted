#!/usr/bin/env bash
# scripts/test-integration.sh — run perfuncted integration tests
#
# Usage:
#   bash scripts/test-integration.sh headless   # Run in a new isolated headless session (default)
#   bash scripts/test-integration.sh nested     # Start and run a nested session
#   bash scripts/test-integration.sh desktop    # Run against the host desktop
#   bash scripts/test-integration.sh --app kwrite headless # Filter by app

set -euo pipefail
cd "$(dirname "$0")/.."

MODE="headless"

# Parse simple args
# This script now primarily delegates session management to the integration binary.
for arg in "$@"; do
    case "$arg" in
        headless|nested|desktop)
            MODE="$arg"
            ;;
    esac
done

case "$MODE" in
    headless)
        echo "▶ running integration tests in HEADLESS mode"
        go run ./cmd/integration --headless "$@"
        ;;
    nested)
        echo "▶ running integration tests in NESTED mode"
        # cmd/integration starts and owns the nested session
        go run ./cmd/integration --nested "$@"
        ;;
    desktop)
        echo "▶ running integration tests on DESKTOP (host)"
        PF_TEST_PREFIX="desktop"
        export PF_TEST_PREFIX
        go run ./cmd/integration "$@"
        ;;
    *)
        echo "Usage: $0 {headless|nested|desktop} [--app NAME]" >&2
        exit 2
        ;;
esac
