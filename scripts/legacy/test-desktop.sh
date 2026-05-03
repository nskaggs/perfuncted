#!/usr/bin/env bash
# scripts/test-desktop.sh — integration test against a display backend.
#
# Runs the shared Go integration suite against the requested display backend.
# This exercises the same scenarios as CI:
#
#   KDE Plasma Wayland  →  KWinShot + WlVirtual + KWinScriptManager
#   X11 / XWayland      →  X11Backend + XTest    + X11Backend
#
# IMPORTANT: the test moves the mouse, types text, and opens/closes kwrite
# and pluma.  Do not touch the keyboard or mouse while it is running.
# Move to a clear workspace before starting.

set -uo pipefail
cd "$(dirname "$0")/.."

MODE="${PF_TEST_DISPLAY_SERVER:-wayland}"

echo ""
echo "══════════════════════════════════════════════"
echo " perfuncted desktop integration test"
echo "══════════════════════════════════════════════"
echo ""
echo "  PF_TEST_DISPLAY_SERVER=$MODE"
echo ""
echo "  WARNING: the test will move the mouse, type text, and open"
echo "  windows on this desktop.  Do not touch the keyboard or mouse."
echo ""

echo "  Starting now..."
echo ""

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
PF_TEST_DISPLAY_SERVER="$MODE" go test -tags=integration ./integration 2>&1 || RC=$?

# The harness exit code is sufficient.
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
echo ""
[ "$FAIL" -eq 0 ]
