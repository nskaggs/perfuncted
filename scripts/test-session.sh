#!/usr/bin/env bash
# scripts/test-session.sh — tests the session package's Start/Perfuncted/Stop lifecycle.
#
# This runs OUTSIDE any existing sway session — it creates its own via the
# session package. Requires: dbus-daemon, sway, wl-paste, kwrite in PATH.
#
# Usage:
#   bash scripts/test-session.sh

set -euo pipefail
cd "$(dirname "$0")/.."

echo "▶ building session test binary..."
go build -o /tmp/pf-session-test ./cmd/session-test
echo "  done"

echo "▶ running session lifecycle test..."
/tmp/pf-session-test
RC=$?
rm -f /tmp/pf-session-test

if [ "$RC" -eq 0 ]; then
    echo ""
    echo "══════════════════════════════════════════════"
    echo "  SESSION TEST PASSED"
    echo "══════════════════════════════════════════════"
else
    echo ""
    echo "══════════════════════════════════════════════"
    echo "  SESSION TEST FAILED (rc=$RC)"
    echo "══════════════════════════════════════════════"
fi
exit "$RC"
