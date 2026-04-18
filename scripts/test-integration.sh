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

        # Always start a fresh temporary nested session for each run. Do not reuse
        # any existing /tmp/perfuncted-xdg-* directories to avoid state leakage.
        HOST_XDG="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
        HOST_WL="${WAYLAND_DISPLAY:-wayland-0}"
        MY_XDG=$(mktemp -d -p /tmp perfuncted-xdg-XXXXXX)
        chmod 0700 "$MY_XDG"
        mkdir -p /tmp/perfuncted-logs

        # start dbus-daemon in the nested XDG dir
        DBUS_ADDR="unix:path=$MY_XDG/bus"
        env -i PATH="$PATH" HOME="$HOME" XDG_RUNTIME_DIR="$MY_XDG" \
            dbus-daemon --session --address="$DBUS_ADDR" --nofork --print-address \
            >/dev/null 2>&1 &
        DBUS_PID="$!"

        # wait for dbus socket to become available
        wait_for_dbus() {
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
        if ! wait_for_dbus "$DBUS_ADDR"; then
            echo "error: dbus failed to start in nested session" >&2
            rm -rf "$MY_XDG"
            exit 1
        fi

        SWAY_WL="wayland-1"
        SWAY_LOG="/tmp/perfuncted-logs/sway-nested.log"
        XDG_RUNTIME_DIR="$MY_XDG" \
            WAYLAND_DISPLAY="$HOST_XDG/$HOST_WL" \
            DBUS_SESSION_BUS_ADDRESS="$DBUS_ADDR" \
            WLR_BACKENDS=wayland WLR_RENDERER=pixman \
            sway --unsupported-gpu -c config/sway/nested.conf \
            > "$SWAY_LOG" 2>&1 &
        SWAY_PID="$!"

        wait_for_socket() {
            local path="$1" secs="$2" i=0
            while [ $i -lt $((secs * 10)) ]; do
                [ -S "$path" ] && return 0
                sleep 0.1; i=$((i + 1))
            done
            return 1
        }
        if ! wait_for_socket "$MY_XDG/$SWAY_WL" 30; then
            echo "error: sway Wayland socket did not appear within 30s" >&2
            cat "$SWAY_LOG" >&2
            kill "$SWAY_PID" 2>/dev/null || true
            rm -rf "$MY_XDG"
            exit 1
        fi

        # start wl-paste in the nested session
        XDG_RUNTIME_DIR="$MY_XDG" WAYLAND_DISPLAY="$SWAY_WL" wl-paste --watch cat >/dev/null 2>&1 &
        WLPID="$!"

        cleanup_nested() {
            [ -n "$WLPID" ] && kill "$WLPID" 2>/dev/null || true
            [ -n "$SWAY_PID" ] && kill "$SWAY_PID" 2>/dev/null || true
            [ -n "$DBUS_PID" ] && kill "$DBUS_PID" 2>/dev/null || true
            if [ "${PRESERVE_XDG:-0}" -eq 0 ]; then
                rm -rf "$MY_XDG" 2>/dev/null || true
            else
                echo "Preserving nested XDG dir: $MY_XDG" > /tmp/perfuncted-logs/preserve.txt || true
            fi
        }
        trap cleanup_nested EXIT

        echo "  nested session started at $MY_XDG (will be cleaned up on exit unless PRESERVE_XDG=1)"

        export XDG_RUNTIME_DIR="$MY_XDG"
        export WAYLAND_DISPLAY="$SWAY_WL"
        export DBUS_SESSION_BUS_ADDRESS="$DBUS_ADDR"

        go run ./cmd/integration "$@"
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
