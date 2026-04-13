#!/usr/bin/env bash
# scripts/test-wayland.sh — fast single-session Wayland integration test
#
# Usage:
#   bash scripts/test-wayland.sh             # headless sway + kwrite (default)
#   bash scripts/test-wayland.sh nested      # visible sway on host Wayland + kwrite
#
# Both modes run in a fully isolated XDG_RUNTIME_DIR so host processes
# are never affected. Wall time: under 2 minutes.

set -euo pipefail
cd "$(dirname "$0")/.."

MODE="${1:-headless}"   # headless | nested
APP="${2:-kwrite}"

mkdir -p /tmp/perfuncted-logs

echo "▶ building..."
go build -o /tmp/pf-integration-bin ./cmd/integration
go build -o /tmp/pf-bin ./cmd/pf
echo "  done"

# ── helpers ───────────────────────────────────────────────────────────────────

wait_socket() {
    local path="$1" secs="$2" i=0
    while [ $i -lt $((secs * 10)) ]; do
        [ -S "$path" ] && return 0
        sleep 0.1; i=$((i + 1))
    done
    return 1
}

wait_dbus() {
    local addr="$1" i=0
    while [ $i -lt 100 ]; do
        DBUS_SESSION_BUS_ADDRESS="$addr" dbus-send \
            --session --dest=org.freedesktop.DBus \
            --type=method_call --print-reply \
            /org/freedesktop/DBus org.freedesktop.DBus.ListNames \
            >/dev/null 2>&1 && return 0
        sleep 0.1; i=$((i + 1))
    done
    return 1
}

# ── isolated session ──────────────────────────────────────────────────────────

HOST_XDG="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
HOST_WL="${WAYLAND_DISPLAY:-wayland-0}"

MY_XDG=$(mktemp -d -p /tmp perfuncted-xdg-XXXXXX)
chmod 0700 "$MY_XDG"

SWAY_PID="" WLC_PID="" DBUS_PID=""

cleanup() {
    [ -n "$WLC_PID"  ] && kill "$WLC_PID"  2>/dev/null || true
    [ -n "$SWAY_PID" ] && kill "$SWAY_PID" 2>/dev/null || true
    [ -n "$DBUS_PID" ] && kill "$DBUS_PID" 2>/dev/null || true
    rm -rf "$MY_XDG" 2>/dev/null || true
}
trap cleanup EXIT

DBUS_ADDR="unix:path=$MY_XDG/bus"
env -i PATH="$PATH" HOME="$HOME" XDG_RUNTIME_DIR="$MY_XDG" \
    dbus-daemon --session --address="$DBUS_ADDR" --nofork --print-address \
    >/dev/null 2>&1 &
DBUS_PID=$!

if ! wait_dbus "$DBUS_ADDR"; then
    echo "✗ dbus failed to start" >&2; exit 1
fi

SWAY_WL="wayland-1"

if [ "$MODE" = "nested" ]; then
    echo "▶ starting nested sway (visible window on $HOST_WL)..."
    SWAY_LOG=/tmp/perfuncted-logs/sway-nested.log
    XDG_RUNTIME_DIR="$MY_XDG" \
    WAYLAND_DISPLAY="$HOST_XDG/$HOST_WL" \
    DBUS_SESSION_BUS_ADDRESS="$DBUS_ADDR" \
    WLR_BACKENDS=wayland WLR_RENDERER=pixman \
        sway --unsupported-gpu -c config/sway/nested.conf \
        > "$SWAY_LOG" 2>&1 &
else
    echo "▶ starting headless sway..."
    SWAY_LOG=/tmp/perfuncted-logs/sway-headless.log
    XDG_RUNTIME_DIR="$MY_XDG" \
    DBUS_SESSION_BUS_ADDRESS="$DBUS_ADDR" \
    WLR_BACKENDS=headless WLR_RENDERER=pixman \
        sway --unsupported-gpu -c config/sway/ci.conf \
        > "$SWAY_LOG" 2>&1 &
fi
SWAY_PID=$!

if ! wait_socket "$MY_XDG/$SWAY_WL" 30; then
    echo "✗ sway Wayland socket did not appear within 30s" >&2
    cat "$SWAY_LOG" >&2
    exit 1
fi
echo "  sway ready ($MY_XDG/$SWAY_WL)"

XDG_RUNTIME_DIR="$MY_XDG" WAYLAND_DISPLAY="$SWAY_WL" \
    wl-paste --watch cat >/dev/null 2>&1 &
WLC_PID=$!
sleep 1

# ── integration test ──────────────────────────────────────────────────────────

echo "▶ running integration test ($APP, mode=$MODE)..."
LOG="/tmp/perfuncted-logs/wayland-${MODE}-test.log"
INTEGRATION_RC=0

env -i \
    PATH="$PATH" HOME="$HOME" \
    XDG_RUNTIME_DIR="$MY_XDG" \
    DBUS_SESSION_BUS_ADDRESS="$DBUS_ADDR" \
    WAYLAND_DISPLAY="$SWAY_WL" \
    DISPLAY="" \
    GDK_BACKEND=wayland \
    QT_QPA_PLATFORM=wayland \
    PF_TEST_PREFIX="wayland-${MODE}-${APP}" \
    /tmp/pf-integration-bin --app "$APP" 2>&1 | tee "$LOG" || INTEGRATION_RC=$?

# ── CLI smoke tests ───────────────────────────────────────────────────────────

echo ""
echo "▶ CLI smoke tests..."
CLI_RC=0

run_pf() {
    env -i PATH="$PATH" HOME="$HOME" \
        XDG_RUNTIME_DIR="$MY_XDG" \
        DBUS_SESSION_BUS_ADDRESS="$DBUS_ADDR" \
        WAYLAND_DISPLAY="$SWAY_WL" \
        DISPLAY="" GDK_BACKEND=wayland QT_QPA_PLATFORM=wayland \
        /tmp/pf-bin "$@"
}

run_pf screen grab --rect 0,0,10,10 --out /tmp/pf-test-grab.png >/dev/null 2>&1 \
    && echo "✓ pf screen grab" || { echo "✗ pf screen grab"; CLI_RC=1; }

H=$(run_pf find pixel-hash --rect 0,0,10,10 2>/dev/null)
[ -n "$H" ] && echo "✓ pf find pixel-hash ($H)" || { echo "✗ pf find pixel-hash"; CLI_RC=1; }

run_pf find wait-for --rect 0,0,10,10 --hash "$H" --timeout 1s >/dev/null 2>&1 \
    && echo "✓ pf find wait-for" || { echo "✗ pf find wait-for"; CLI_RC=1; }

run_pf find wait-for-no-change --rect 0,0,10,10 --stable 3 --poll 100ms --timeout 3s >/dev/null 2>&1 \
    && echo "✓ pf find wait-for-no-change" || { echo "✗ pf find wait-for-no-change"; CLI_RC=1; }

run_pf input move --x 100 --y 100 >/dev/null 2>&1 \
    && echo "✓ pf input move" || { echo "✗ pf input move"; CLI_RC=1; }

run_pf window list >/dev/null 2>&1 \
    && echo "✓ pf window list" || { echo "✗ pf window list"; CLI_RC=1; }

run_pf info >/dev/null 2>&1 \
    && echo "✓ pf info" || { echo "✗ pf info"; CLI_RC=1; }

# ── result ────────────────────────────────────────────────────────────────────

echo ""
echo "══════════════════════════════════════════════"
if [ "$INTEGRATION_RC" -eq 0 ] && [ "$CLI_RC" -eq 0 ]; then
    echo "  PASSED (mode=$MODE, app=$APP)"
    echo "══════════════════════════════════════════════"
    exit 0
else
    echo "  FAILED (integration rc=$INTEGRATION_RC, cli rc=$CLI_RC)"
    echo "══════════════════════════════════════════════"
    echo "  logs: $LOG"
    [ -f "$SWAY_LOG" ] && echo "  sway: $SWAY_LOG"
    exit 1
fi
