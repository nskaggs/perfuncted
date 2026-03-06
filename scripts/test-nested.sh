#!/usr/bin/env bash
# scripts/test-nested.sh — nested integration test suite
#
# Runs cmd/integration against three isolated nested sessions concurrently:
#   1. Nested Wayland  (sway WLR_BACKENDS=headless)
#   2. Nested XWayland (sway WLR_BACKENDS=headless + XWayland)
#   3. Nested X11      (sway WLR_BACKENDS=x11 inside Xvfb — no host display)

set -euo pipefail
cd "$(dirname "$0")/.."

# ── environment sanity check ──────────────────────────────────────────────────

echo ""
echo "══════════════════════════════════════════════"
echo " perfuncted nested integration test suite (PARALLEL)"
echo "══════════════════════════════════════════════"
echo ""
echo "  host WAYLAND_DISPLAY=$WAYLAND_DISPLAY"
echo "  host DISPLAY=$DISPLAY"
echo "  host XDG_RUNTIME_DIR=$XDG_RUNTIME_DIR"

if [ -z "$WAYLAND_DISPLAY" ] || [ ! -S "$XDG_RUNTIME_DIR/$WAYLAND_DISPLAY" ]; then
    echo "FATAL: host Wayland socket not found — cannot continue"
    exit 1
fi

HOST_WL="$WAYLAND_DISPLAY"
mkdir -p /tmp/perfuncted-logs
rm -f /tmp/perfuncted-logs/*.log /tmp/perfuncted-logs/*.res

echo "  building pf CLI binary for tests..."
go build -o /tmp/pf-bin ./cmd/pf/

# ── shared helpers ────────────────────────────────────────────────────────────

wait_socket() {
    local path="$1" secs="$2" i=0
    while [ $i -lt $((secs*10)) ]; do
        [ -S "$path" ] && return 0
        sleep 0.1
        i=$((i+1))
    done
    return 1
}

wait_no_socket() {
    local path="$1" secs="$2" i=0
    while [ $i -lt $((secs*10)) ]; do
        [ ! -e "$path" ] && return 0
        sleep 0.1
        i=$((i+1))
    done
    return 1
}

# clean_run: executes a command with a whitelist-only environment to prevent leaks from host
# Usage: clean_run [VAR=VAL ...] command [args...]
clean_run() {
    local env_pairs=()
    while [[ $# -gt 0 && "$1" == *"="* ]]; do
        env_pairs+=("$1")
        shift
    done
    
    # Base whitelist: PATH, HOME, XDG_RUNTIME_DIR, DBUS_SESSION_BUS_ADDRESS, PF_TEST_PREFIX
    # Plus any additional vars passed in before the command.
    env -i \
        PATH="$PATH" \
        HOME="$HOME" \
        XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR" \
        DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-}" \
        PF_TEST_PREFIX="${PF_TEST_PREFIX:-}" \
        "${env_pairs[@]}" \
        "$@"
}

run_example() {
    local env_str="$1"
    EXAMPLE_RC=0
    # eval is needed to split env_str into arguments for clean_run
    # we use bash -c to handle redirections inside the clean environment
    eval "clean_run $env_str bash -c 'go run ./cmd/integration/ 2>&1'" || EXAMPLE_RC=$?
}

test_cli() {
    local env_str="$1"
    local prefix="$2"
    CLI_RC=0
    
    clean_run $env_str bash -c "/tmp/pf-bin screen grab --rect 0,0,10,10 --out /tmp/pf-test-$prefix.png >/dev/null 2>&1"
    local rc=$?
    if [ $rc -eq 0 ]; then ok "CLI: pf screen grab"; else fail "CLI: pf screen grab ($rc)"; CLI_RC=1; fi

    local h
    h=$(clean_run $env_str bash -c "/tmp/pf-bin find pixel-hash --rect 0,0,10,10 2>/dev/null")
    if [ -n "$h" ]; then ok "CLI: pf find pixel-hash ($h)"; else fail "CLI: pf find pixel-hash"; CLI_RC=1; fi

    clean_run $env_str bash -c "/tmp/pf-bin find wait-for --rect 0,0,10,10 --hash '$h' --timeout 1s >/dev/null 2>&1"
    rc=$?
    if [ $rc -eq 0 ]; then ok "CLI: pf find wait-for"; else fail "CLI: pf find wait-for ($rc)"; CLI_RC=1; fi

    clean_run $env_str bash -c "/tmp/pf-bin find scan-for --rects '0,0,10,10;10,10,20,20' --wants '$h,00000000' --timeout 1s >/dev/null 2>&1"
    rc=$?
    if [ $rc -eq 0 ]; then ok "CLI: pf find scan-for"; else fail "CLI: pf find scan-for ($rc)"; CLI_RC=1; fi

    clean_run $env_str bash -c "/tmp/pf-bin input move --x 100 --y 100 >/dev/null 2>&1"
    rc=$?
    if [ $rc -eq 0 ]; then ok "CLI: pf input move"; else fail "CLI: pf input move ($rc)"; CLI_RC=1; fi
    
    clean_run $env_str bash -c "/tmp/pf-bin window list >/dev/null 2>&1"
    rc=$?
    if [ $rc -eq 0 ]; then ok "CLI: pf window list"; else fail "CLI: pf window list ($rc)"; CLI_RC=1; fi

    # pf info: verify backend probe output
    clean_run $env_str bash -c "/tmp/pf-bin info >/dev/null 2>&1"
    rc=$?
    if [ $rc -eq 0 ]; then ok "CLI: pf info"; else fail "CLI: pf info ($rc)"; CLI_RC=1; fi
}

# ── session workers ───────────────────────────────────────────────────────────

run_session_wayland() {
    local PASS=0 FAIL=0 SWAY_PID="" SWAY_WL="wayland-1" PREFIX="wayland"
    export PF_TEST_PREFIX="$PREFIX"
    
    local MY_XDG
    MY_XDG=$(mktemp -d -p /tmp perfuncted-xdg-$PREFIX-XXXXXX)
    chmod 0700 "$MY_XDG"
    export XDG_RUNTIME_DIR="$MY_XDG"
    export DBUS_SESSION_BUS_ADDRESS="unix:path=$MY_XDG/bus"
    
    # Start D-Bus cleanly
    clean_run dbus-daemon --session --address=$DBUS_SESSION_BUS_ADDRESS --nofork --print-address &
    DBUS_PID=$!
    
    log()  { echo "[$PREFIX] $*"; }
    ok()   { echo "[$PREFIX] ✓ $*"; PASS=$((PASS+1)); }
    fail() { echo "[$PREFIX] ✗ $*"; FAIL=$((FAIL+1)); }
    
    log "starting sway (WLR_BACKENDS=headless) → $SWAY_WL"
    clean_run WLR_BACKENDS=headless WLR_RENDERER=pixman \
        bash -c "sway --unsupported-gpu -c config/sway/nested.conf > /tmp/perfuncted-logs/sway-$PREFIX.log 2>&1" &
    SWAY_PID=$!

    WLC_PID=""
    if wait_socket "$XDG_RUNTIME_DIR/$SWAY_WL" 60; then
        export WAYLAND_DISPLAY="$SWAY_WL"
        clean_run WAYLAND_DISPLAY="$SWAY_WL" wl-paste --watch cat >/dev/null 2>&1 &
        WLC_PID=$!
        sleep 2
        run_example "WAYLAND_DISPLAY=$SWAY_WL DISPLAY= GDK_BACKEND=wayland"
        test_cli "WAYLAND_DISPLAY=$SWAY_WL DISPLAY= GDK_BACKEND=wayland" "$PREFIX"
        if [ "$EXAMPLE_RC" -eq 0 ] && [ "$CLI_RC" -eq 0 ]; then
            ok "example & CLI exited 0"
        else
            fail "example ($EXAMPLE_RC) or CLI ($CLI_RC) failed"
        fi
    else
        fail "wayland socket $SWAY_WL did not appear within 60 s"
        ls -la "$XDG_RUNTIME_DIR" >> /tmp/perfuncted-logs/sway-$PREFIX.log
    fi
    
    kill "$SWAY_PID" 2>/dev/null || true
    [ -n "$WLC_PID" ] && kill "$WLC_PID" 2>/dev/null || true
    kill "$DBUS_PID" 2>/dev/null || true
    wait_no_socket "$XDG_RUNTIME_DIR/$SWAY_WL" 10 || true
    ok "sway session stopped"
    echo "$PASS $FAIL" > /tmp/perfuncted-logs/$PREFIX.res
    
    if [ -d "$MY_XDG/gvfs" ]; then
        fusermount -u "$MY_XDG/gvfs" 2>/dev/null || true
    fi
    rm -rf "$MY_XDG" 2>/dev/null || true
}

run_session_xwayland() {
    local PASS=0 FAIL=0 SWAY_PID="" SWAY_WL="wayland-1" PREFIX="xwayland"
    export PF_TEST_PREFIX="$PREFIX"
    
    local MY_XDG
    MY_XDG=$(mktemp -d -p /tmp perfuncted-xdg-$PREFIX-XXXXXX)
    chmod 0700 "$MY_XDG"
    export XDG_RUNTIME_DIR="$MY_XDG"
    export DBUS_SESSION_BUS_ADDRESS="unix:path=$MY_XDG/bus"

    clean_run dbus-daemon --session --address=$DBUS_SESSION_BUS_ADDRESS --nofork --print-address 1 &
    DBUS_PID=$!
    
    log()  { echo "[$PREFIX] $*"; }
    ok()   { echo "[$PREFIX] ✓ $*"; PASS=$((PASS+1)); }
    fail() { echo "[$PREFIX] ✗ $*"; FAIL=$((FAIL+1)); }
    
    log "starting sway (WLR_BACKENDS=headless) → Wayland=$SWAY_WL"
    clean_run WLR_BACKENDS=headless WLR_RENDERER=pixman XWAYLAND_NO_AUTH=1 \
        bash -c "sway --unsupported-gpu -c config/sway/nested.conf > /tmp/perfuncted-logs/sway-$PREFIX.log 2>&1" &
    SWAY_PID=$!

    WLC_PID=""
    if wait_socket "$XDG_RUNTIME_DIR/$SWAY_WL" 60; then
        # XWayland display number in clean env is usually :0.
        local XSOCK="/tmp/.X11-unix/X0"
        if wait_socket "$XSOCK" 60; then
            export WAYLAND_DISPLAY="$SWAY_WL"
            export DISPLAY=":0"
            clean_run WAYLAND_DISPLAY="$SWAY_WL" DISPLAY=":0" wl-paste --watch cat >/dev/null 2>&1 &
            WLC_PID=$!
            sleep 2
            run_example "WAYLAND_DISPLAY=$SWAY_WL DISPLAY=:0 GDK_BACKEND=x11"
            test_cli "WAYLAND_DISPLAY=$SWAY_WL DISPLAY=:0" "$PREFIX"
            if [ "$EXAMPLE_RC" -eq 0 ] && [ "$CLI_RC" -eq 0 ]; then
                ok "example & CLI exited 0"
            else
                fail "example ($EXAMPLE_RC) or CLI ($CLI_RC) failed"
            fi
        else
            fail "XWayland socket $XSOCK did not appear within 60 s"
        fi
    else
        fail "wayland socket $SWAY_WL did not appear within 60 s"
        ls -la "$XDG_RUNTIME_DIR" >> /tmp/perfuncted-logs/sway-$PREFIX.log
    fi
    kill "$SWAY_PID" 2>/dev/null || true
    [ -n "$WLC_PID" ] && kill "$WLC_PID" 2>/dev/null || true
    kill "$DBUS_PID" 2>/dev/null || true
    wait_no_socket "$XDG_RUNTIME_DIR/$SWAY_WL" 10 || true
    ok "sway session stopped"
    echo "$PASS $FAIL" > /tmp/perfuncted-logs/$PREFIX.res
    
    if [ -d "$MY_XDG/gvfs" ]; then
        fusermount -u "$MY_XDG/gvfs" 2>/dev/null || true
    fi
    rm -rf "$MY_XDG" 2>/dev/null || true
}

run_session_xvfb() {
    local PASS=0 FAIL=0 SWAY_PID="" SWAY_WL="wayland-1" PREFIX="xvfb"
    local XVFB_DISP=":99" XVFB_PID=""
    export PF_TEST_PREFIX="$PREFIX"

    local MY_XDG
    MY_XDG=$(mktemp -d -p /tmp perfuncted-xdg-$PREFIX-XXXXXX)
    chmod 0700 "$MY_XDG"
    export XDG_RUNTIME_DIR="$MY_XDG"
    export DBUS_SESSION_BUS_ADDRESS="unix:path=$MY_XDG/bus"

    clean_run dbus-daemon --session --address=$DBUS_SESSION_BUS_ADDRESS --nofork --print-address 1 &
    DBUS_PID=$!

    log()  { echo "[$PREFIX] $*"; }
    ok()   { echo "[$PREFIX] ✓ $*"; PASS=$((PASS+1)); }
    fail() { echo "[$PREFIX] ✗ $*"; FAIL=$((FAIL+1)); }

    log "starting Xvfb $XVFB_DISP"
    clean_run bash -c "Xvfb $XVFB_DISP -screen 0 1024x768x24 > /tmp/perfuncted-logs/xvfb.log 2>&1" &
    XVFB_PID=$!
    sleep 3

    log "starting sway (WLR_BACKENDS=x11, DISPLAY=$XVFB_DISP) → $SWAY_WL"
    clean_run DISPLAY="$XVFB_DISP" WLR_BACKENDS=x11 WLR_RENDERER=pixman \
        bash -c "sway --unsupported-gpu -c config/sway/nested.conf > /tmp/perfuncted-logs/sway-$PREFIX.log 2>&1" &
    SWAY_PID=$!

    WLC_PID=""
    if wait_socket "$XDG_RUNTIME_DIR/$SWAY_WL" 60; then
        export WAYLAND_DISPLAY="$SWAY_WL"
        clean_run WAYLAND_DISPLAY="$SWAY_WL" DISPLAY="$XVFB_DISP" wl-paste --watch cat >/dev/null 2>&1 &
        WLC_PID=$!
        sleep 2
        run_example "WAYLAND_DISPLAY=$SWAY_WL DISPLAY=$XVFB_DISP GDK_BACKEND=x11"
        test_cli "WAYLAND_DISPLAY=$SWAY_WL DISPLAY=$XVFB_DISP" "$PREFIX"
        if [ "$EXAMPLE_RC" -eq 0 ] && [ "$CLI_RC" -eq 0 ]; then
            ok "example & CLI exited 0"
        else
            fail "example ($EXAMPLE_RC) or CLI ($CLI_RC) failed"
        fi
    else
        fail "wayland socket $SWAY_WL did not appear within 60 s"
        ls -la "$XDG_RUNTIME_DIR" >> /tmp/perfuncted-logs/sway-$PREFIX.log
    fi

    kill "$SWAY_PID" 2>/dev/null || true
    [ -n "$WLC_PID" ] && kill "$WLC_PID" 2>/dev/null || true
    [ -n "$XVFB_PID" ] && kill "$XVFB_PID" 2>/dev/null || true
    kill "$DBUS_PID" 2>/dev/null || true
    wait_no_socket "$XDG_RUNTIME_DIR/$SWAY_WL" 10 || true
    ok "sway session stopped"
    echo "$PASS $FAIL" > /tmp/perfuncted-logs/$PREFIX.res
    
    if [ -d "$MY_XDG/gvfs" ]; then
        fusermount -u "$MY_XDG/gvfs" 2>/dev/null || true
    fi
    rm -rf "$MY_XDG" 2>/dev/null || true
}

# ── execute concurrently ──────────────────────────────────────────────────────

echo "  launching sessions concurrently..."
( run_session_wayland  > /tmp/perfuncted-logs/wayland-test.log  2>&1 ) &
PID1=$!
( run_session_xwayland > /tmp/perfuncted-logs/xwayland-test.log 2>&1 ) &
PID2=$!
( run_session_xvfb     > /tmp/perfuncted-logs/xvfb-test.log     2>&1 ) &
PID3=$!

wait $PID1 $PID2 $PID3 || true

# ── aggregate results ─────────────────────────────────────────────────────────

TOTAL_PASS=0
TOTAL_FAIL=0

echo ""
for pfx in wayland xwayland xvfb; do
    echo "── SESSION: $pfx ────────────────────────────────────────"
    cat /tmp/perfuncted-logs/$pfx-test.log
    if [ -f /tmp/perfuncted-logs/$pfx.res ]; then
        read p f < /tmp/perfuncted-logs/$pfx.res
        TOTAL_PASS=$((TOTAL_PASS + p))
        TOTAL_FAIL=$((TOTAL_FAIL + f))
    else
        echo "  [$pfx] ✗ FATAL: session crashed without writing results"
        TOTAL_FAIL=$((TOTAL_FAIL + 1))
    fi
    echo ""
done

echo "══════════════════════════════════════════════"
printf "  passed: %d   failed: %d\n" "$TOTAL_PASS" "$TOTAL_FAIL"
echo "══════════════════════════════════════════════"
echo "  logs: /tmp/perfuncted-logs/"
[ "$TOTAL_FAIL" -eq 0 ]
