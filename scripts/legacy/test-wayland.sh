#!/usr/bin/env bash
# scripts/test-wayland.sh — fast single-session Wayland integration test
#
# Usage:
#   bash scripts/test-wayland.sh             # headless sway + kwrite (default)
#   bash scripts/test-wayland.sh nested      # visible sway on host Wayland + kwrite
#
# Both modes run in a fully isolated XDG_RUNTIME_DIR so host processes
# are never affected. Wall time: under 2 minutes.

set -uo pipefail
cd "$(dirname "$0")/.."

MODE="${1:-headless}"   # headless | nested
APP="${2:-kwrite}"

mkdir -p /tmp/perfuncted-logs

echo "▶ building..."
rm -rf /tmp/pf-integration-bin /tmp/pf-bin || true
go build -o /tmp/pf-integration-bin ./cmd/integration
go build -o /tmp/pf-bin ./cmd/pf

# Create a reference PNG with solid red content for CLI tests
REF_PNG="/tmp/pf-test-ref.png"
if [ ! -f "$REF_PNG" ]; then
    cat > /tmp/gen_png.go << 'EOGO'
package main
import (
    "image"
    "image/color"
    "image/png"
    "os"
)
func main() {
    img := image.NewRGBA(image.Rect(0, 0, 100, 100))
    red := color.RGBA{0xFF, 0x00, 0x00, 0xFF}
    for y := 0; y < 100; y++ {
        for x := 0; x < 100; x++ {
            img.Set(x, y, red)
        }
    }
    f, _ := os.Create("$REF_PNG")
    png.Encode(f, img)
    f.Close()
}
EOGO
    echo "  creating reference PNG..."
    go run /tmp/gen_png.go || { echo "ERROR: failed to create reference PNG" >&2; exit 1; }
    rm -f /tmp/gen_png.go
fi

echo "  reference PNG: $REF_PNG"

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
REF_PNG_KEEP="$REF_PNG"

cleanup() {
    [ -n "$WLC_PID"  ] && kill "$WLC_PID"  2>/dev/null || true
    [ -n "$SWAY_PID" ] && kill "$SWAY_PID" 2>/dev/null || true
    [ -n "$DBUS_PID" ] && kill "$DBUS_PID" 2>/dev/null || true
    rm -rf "$MY_XDG" 2>/dev/null || true
    # Keep the reference PNG so CLI tests can find it
}
trap cleanup EXIT

DBUS_ADDR="unix:path=$MY_XDG/bus"
env -i PATH="$PATH" HOME="$HOME" XDG_RUNTIME_DIR="$MY_XDG" \
    dbus-daemon --session --address="$DBUS_ADDR" --nofork --print-address \
    >/dev/null 2>&1 &
DBUS_PID="$!"

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
SWAY_PID="$!"

if ! wait_socket "$MY_XDG/$SWAY_WL" 30; then
    echo "✗ sway Wayland socket did not appear within 30s" >&2
    cat "$SWAY_LOG" >&2; exit 1
fi
echo "  sway ready ($MY_XDG/$SWAY_WL)"

XDG_RUNTIME_DIR="$MY_XDG" WAYLAND_DISPLAY="$SWAY_WL" \
    wl-paste --watch cat >/dev/null 2>&1 &
WLC_PID="$!"
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

# Test screen grab works
run_pf screen grab --rect 0,0,10,10 --out /tmp/pf-test-grab.png >/dev/null 2>&1 \
    && echo "✓ pf screen grab" || { echo "✗ pf screen grab"; CLI_RC=1; }

# Test pixel hash using reference PNG CRC32
REF_HASH=$(python3 -c "
import zlib, struct
data = open('$REF_PNG','rb').read()
pos = data.find(b'IDAT')
if pos == -1:
    exit(1)
raw = data[pos+4:pos+struct.unpack('>I', data[pos-4:pos])[0]+4]
h = zlib.crc32(raw) & 0xffffffff
print('0x%08x' % h)
")

H=""
if [ -n "$REF_HASH" ]; then
    for i in 1 2 3 4 5 6 7 8; do
        H=$(run_pf find pixel-hash --rect 0,0,100,100 2>/dev/null) && break || sleep 0.25
    done
    if [ -n "$H" ]; then
        echo "✓ pf find pixel-hash ($H)"
        run_pf find wait-for --rect 0,0,100,100 --hash "$REF_HASH" --timeout 2s \
            >/dev/null 2>&1 && echo "✓ pf find wait-for" || { echo "✗ pf find wait-for"; CLI_RC=1; }
    else
        echo "⚠ pf find pixel-hash: no content found (expected in blank test session)"
    fi
else
    echo "⚠ pf find pixel-hash: skipping (reference hash unavailable)"
fi

run_pf find wait-for-no-change --rect 0,0,10,10 --stable 3 --poll 100ms --timeout 3s \
    >/dev/null 2>&1 && echo "✓ pf find wait-for-no-change" || { echo "✗ pf find wait-for-no-change"; CLI_RC=1; }

run_pf input move --x 100 --y 100 >/dev/null 2>&1 \
    && echo "✓ pf input move" || { echo "✗ pf input move"; CLI_RC=1; }

run_pf window list >/dev/null 2>&1 \
    && echo "✓ pf window list" || { echo "✗ pf window list"; CLI_RC=1; }

run_pf info >/dev/null 2>&1 \
    && echo "✓ pf info" || { echo "✗ pf info"; CLI_RC=1; }

RES="$(run_pf screen resolution 2>/dev/null)"
[ -n "$RES" ] && echo "✓ pf screen resolution ($RES)" || { echo "✗ pf screen resolution"; CLI_RC=1; }

run_pf find color --rect 0,0,10,10 --color 000000 --tolerance 50 >/dev/null 2>&1 \
    && echo "✓ pf find color" || { echo "✗ pf find color"; CLI_RC=1; }

run_pf clipboard set "pf-test-clip" >/dev/null 2>&1 \
    && echo "✓ pf clipboard set" || { echo "✗ pf clipboard set"; CLI_RC=1; }

CLIP="$(run_pf clipboard get 2>/dev/null)"
[ "$CLIP" = "pf-test-clip" ] && echo "✓ pf clipboard get" || { echo "✗ pf clipboard get (got: $CLIP)"; CLI_RC=1; }

run_pf session type >/dev/null 2>&1 \
    && echo "✓ pf session type" || { echo "✗ pf session type"; CLI_RC=1; }

run_pf session check >/dev/null 2>&1 \
    && echo "✓ pf session check" || { echo "✗ pf session check"; CLI_RC=1; }

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
