#!/usr/bin/env bash
# scripts/test-desktop.sh — integration test against the primary desktop.
#
# Runs cmd/integration directly on the current WAYLAND_DISPLAY / DISPLAY without
# starting any nested compositor.  This exercises backends that nested sway
# sessions never reach:
#
#   KDE Plasma Wayland  →  KWinShot + WlVirtual + KWinScriptManager
#   X11 / XWayland      →  X11Backend + XTest    + X11Backend
#
# IMPORTANT: the test moves the mouse, types text, and opens/closes kwrite
# and pluma.  Do not touch the keyboard or mouse while it is running.
# Move to a clear workspace before starting.

set -uo pipefail
cd "$(dirname "$0")/../.."

echo ""
echo "══════════════════════════════════════════════"
echo " perfuncted desktop integration test"
echo "══════════════════════════════════════════════"
echo ""
echo "  WAYLAND_DISPLAY=${WAYLAND_DISPLAY:-<unset>}"
echo "  DISPLAY=${DISPLAY:-<unset>}"
echo ""
echo "  WARNING: the test will move the mouse, type text, and open"
echo "  windows on this desktop.  Do not touch the keyboard or mouse."
echo ""

echo "  Starting now..."
echo ""

mkdir -p /tmp/perfuncted-logs

# If none of the GUI apps we rely on are installed, skip this test.
# This makes 'just test-desktop' safe in CI and headless environments.
if ! command -v kwrite >/dev/null 2>&1 && ! command -v pluma >/dev/null 2>&1 && ! command -v firefox >/dev/null 2>&1; then
    echo "No supported GUI apps (kwrite, pluma, firefox) found in PATH; skipping desktop integration test."
    exit 0
fi

# Kill any leftover test app windows from prior failed runs.
for pat in "kwrite /tmp/perfuncted-kwrite.txt" "pluma /tmp/perfuncted-pluma.txt"; do
    pid=$(pgrep -f "$pat" 2>/dev/null | head -1) && [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
done

# Clean up any leftover test artifacts before running.
for f in ${TMPDIR:-/tmp}/perfuncted-kwrite.txt ${TMPDIR:-/tmp}/perfuncted-pluma.txt; do
    if [ -f "$f" ]; then
        echo "  removing leftover $f"
        rm -f "$f"
    fi
done

PASS=0; FAIL=0; RC=0
go run ./cmd/integration/ 2>&1 || RC=$?

# Parse pass/fail from output (cmd/integration prints its own summary).
# The harness exit code alone is sufficient.
if [ "$RC" -eq 0 ]; then
    echo ""
    echo "  ✓ all tests passed"
    PASS=$((PASS+1))
else
    echo ""
    echo "  ✗ example exited $RC"
    FAIL=$((FAIL+1))
fi

# Remove test artifacts.
rm -f ${TMPDIR:-/tmp}/perfuncted-kwrite.txt ${TMPDIR:-/tmp}/perfuncted-pluma.txt

echo ""
echo "══════════════════════════════════════════════"
printf "  passed: %d   failed: %d\n" "$PASS" "$FAIL"
echo "══════════════════════════════════════════════"
echo "  logs: /tmp/perfuncted-logs/"
echo ""
[ "$FAIL" -eq 0 ]
