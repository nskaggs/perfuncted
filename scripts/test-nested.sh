#!/usr/bin/env bash
# scripts/test-nested.sh — nested integration test suite
#
# Runs cmd/integration against three isolated nested sessions in sequence:
#   1. Nested Wayland  (sway WLR_BACKENDS=wayland, targeting wayland-1)
#   2. Nested XWayland (same sway session, targeting its :1 X display)
#   3. Nested X11      (sway WLR_BACKENDS=x11, targeting wayland-2)
#
# Each session is started by this script, its PID is recorded, and only
# that specific PID is killed during teardown. Nothing outside of what
# this script started is ever touched.

set -euo pipefail
cd "$(dirname "$0")/.."

PASS=0; FAIL=0
SWAY_PID=""    # PID of the sway instance started in the current session
SWAY_WL=""     # Wayland socket name (e.g. wayland-1) created by sway

# ── helpers ──────────────────────────────────────────────────────────────────

log()  { echo "  $*"; }
ok()   { echo "  ✓ $*"; PASS=$((PASS+1)); }
fail() { echo "  ✗ $*"; FAIL=$((FAIL+1)); }

# wait_socket PATH SECONDS — waits until a Unix socket exists at PATH.
wait_socket() {
    local path="$1" secs="$2" i=0
    while [ $i -lt $((secs*10)) ]; do
        [ -S "$path" ] && return 0
        sleep 0.1
        i=$((i+1))
    done
    return 1
}

# wait_no_socket PATH SECONDS — waits until the socket at PATH disappears.
wait_no_socket() {
    local path="$1" secs="$2" i=0
    while [ $i -lt $((secs*10)) ]; do
        [ ! -e "$path" ] && return 0
        sleep 0.1
        i=$((i+1))
    done
    return 1
}

# next_wayland_display — finds the lowest-numbered wayland-N not currently in use.
next_wayland_display() {
    local n=1
    while [ -e "$XDG_RUNTIME_DIR/wayland-$n" ]; do
        n=$((n+1))
    done
    echo "wayland-$n"
}

# next_x_display — finds the lowest-numbered :N X socket not currently in use.
next_x_display() {
    local n=1
    while [ -e "/tmp/.X11-unix/X$n" ]; do
        n=$((n+1))
    done
    echo ":$n"
}

# stop_sway — kills only the sway PID we started and waits for its socket to vanish.
stop_sway() {
    if [ -z "$SWAY_PID" ]; then return; fi
    log "stopping sway PID $SWAY_PID"
    kill "$SWAY_PID" 2>/dev/null || true
    # Wait up to 10 s for sway to exit
    local i=0
    while kill -0 "$SWAY_PID" 2>/dev/null && [ $i -lt 100 ]; do
        sleep 0.1; i=$((i+1))
    done
    if [ -n "$SWAY_WL" ]; then
        wait_no_socket "$XDG_RUNTIME_DIR/$SWAY_WL" 10 || true
    fi
    SWAY_PID=""; SWAY_WL=""
}

# assert_no_test_file — fails loudly if any test artifact already exists.
assert_no_test_file() {
    local fail=0
    for f in ${TMPDIR:-/tmp}/perfuncted-kwrite.txt ${TMPDIR:-/tmp}/perfuncted-pluma.txt; do
        if [ -f "$f" ]; then
            echo "FATAL: $f already exists — previous session did not clean up"
            fail=1
        fi
    done
    [ "$fail" -eq 0 ] || exit 1
}

# run_example ENV_VARS — runs go run ./cmd/integration/ with given env overrides.
# Prints output, returns exit code in $EXAMPLE_RC.
run_example() {
    local env_str="$1"
    EXAMPLE_RC=0
    env $env_str go run ./cmd/integration/ 2>&1 || EXAMPLE_RC=$?
}

# test_cli ENV_VARS — runs a suite of pf CLI commands with given env overrides.
# Returns exit code in $CLI_RC.
test_cli() {
    local env_str="$1"
    CLI_RC=0
    log "running CLI tests with $env_str"

    # Screen grab
    env $env_str /tmp/pf-bin screen grab --rect 0,0,10,10 --out /tmp/pf-test.png >/dev/null 2>&1
    local rc=$?
    if [ $rc -eq 0 ]; then ok "CLI: pf screen grab"; else fail "CLI: pf screen grab ($rc)"; CLI_RC=1; fi

    # Find pixel-hash
    local h
    h=$(env $env_str /tmp/pf-bin find pixel-hash --rect 0,0,10,10 2>/dev/null)
    if [ -n "$h" ]; then ok "CLI: pf find pixel-hash ($h)"; else fail "CLI: pf find pixel-hash"; CLI_RC=1; fi

    # Find wait-for (immediately returns since hash is current)
    env $env_str /tmp/pf-bin find wait-for --rect 0,0,10,10 --hash "$h" --timeout 1s >/dev/null 2>&1
    rc=$?
    if [ $rc -eq 0 ]; then ok "CLI: pf find wait-for"; else fail "CLI: pf find wait-for ($rc)"; CLI_RC=1; fi

    # Find scan-for (immediately returns since hash is current)
    env $env_str /tmp/pf-bin find scan-for --rects "0,0,10,10;10,10,20,20" --wants "$h,00000000" --timeout 1s >/dev/null 2>&1
    rc=$?
    if [ $rc -eq 0 ]; then ok "CLI: pf find scan-for"; else fail "CLI: pf find scan-for ($rc)"; CLI_RC=1; fi

    # Input move
    env $env_str /tmp/pf-bin input move --x 100 --y 100 >/dev/null 2>&1
    rc=$?
    if [ $rc -eq 0 ]; then ok "CLI: pf input move"; else fail "CLI: pf input move ($rc)"; CLI_RC=1; fi
    
    # Window list
    env $env_str /tmp/pf-bin window list >/dev/null 2>&1
    rc=$?
    if [ $rc -eq 0 ]; then ok "CLI: pf window list"; else fail "CLI: pf window list ($rc)"; CLI_RC=1; fi
}

# ── environment sanity check ──────────────────────────────────────────────────

echo ""
echo "══════════════════════════════════════════════"
echo " perfuncted nested integration test suite"
echo "══════════════════════════════════════════════"
echo ""
log "host WAYLAND_DISPLAY=$WAYLAND_DISPLAY"
log "host DISPLAY=$DISPLAY"
log "host XDG_RUNTIME_DIR=$XDG_RUNTIME_DIR"

# Verify the host Wayland session is healthy before we do anything.
if [ -z "$WAYLAND_DISPLAY" ] || [ ! -S "$XDG_RUNTIME_DIR/$WAYLAND_DISPLAY" ]; then
    echo "FATAL: host Wayland socket not found — cannot continue"
    exit 1
fi

HOST_WL="$WAYLAND_DISPLAY"   # e.g. wayland-0 — we never touch this

# Pre-flight: ensure the sway log directory exists
mkdir -p /tmp/perfuncted-logs

log "building pf CLI binary for tests..."
go build -o /tmp/pf-bin ./cmd/pf/

# ── SESSION 1: Nested Wayland ─────────────────────────────────────────────────

echo ""
echo "── SESSION 1: Nested Wayland (headless) ─────────────────────"
assert_no_test_file

SWAY_WL="$(next_wayland_display)"
log "starting sway (WLR_BACKENDS=headless) → $SWAY_WL"

WAYLAND_DISPLAY="$HOST_WL" WLR_BACKENDS=headless WLR_RENDERER=pixman \
    sway --unsupported-gpu -c config/sway/nested.conf \
    > /tmp/perfuncted-logs/sway-wl.log 2>&1 &
SWAY_PID=$!
log "sway PID=$SWAY_PID"

if wait_socket "$XDG_RUNTIME_DIR/$SWAY_WL" 15; then
    ok "wayland socket $SWAY_WL ready"
    sleep 1  # give sway time to finish compositor init
    log "running example with WAYLAND_DISPLAY=$SWAY_WL"
    run_example "WAYLAND_DISPLAY=$SWAY_WL"
    test_cli "WAYLAND_DISPLAY=$SWAY_WL"
    if [ "$EXAMPLE_RC" -eq 0 ] && [ "$CLI_RC" -eq 0 ]; then
        ok "example & CLI exited 0"
    else
        fail "example ($EXAMPLE_RC) or CLI ($CLI_RC) failed"
    fi
else
    fail "wayland socket $SWAY_WL did not appear within 15 s"
    cat /tmp/perfuncted-logs/sway-wl.log
fi

stop_sway
ok "sway session 1 stopped"
rm -f ${TMPDIR:-/tmp}/perfuncted-kwrite.txt ${TMPDIR:-/tmp}/perfuncted-pluma.txt

# ── SESSION 2: Nested XWayland ────────────────────────────────────────────────

echo ""
echo "── SESSION 2: Nested XWayland (headless) ────────────────────"
assert_no_test_file

# XWayland is embedded in sway. We use Wayland protocols for the library
# (screencopy, virtual input, sway window manager). kwrite uses Wayland natively;
# pluma (GTK3) is forced to XWayland via GDK_BACKEND=x11 to exercise that path.
SWAY_WL="$(next_wayland_display)"
SWAY_XDISP="$(next_x_display)"
log "starting sway (WLR_BACKENDS=headless) → Wayland=$SWAY_WL, XWayland=$SWAY_XDISP"

WAYLAND_DISPLAY="$HOST_WL" WLR_BACKENDS=headless WLR_RENDERER=pixman \
    XWAYLAND_NO_AUTH=1 \
    sway --unsupported-gpu -c config/sway/nested.conf \
    > /tmp/perfuncted-logs/sway-xwl.log 2>&1 &
SWAY_PID=$!
log "sway PID=$SWAY_PID"

if wait_socket "$XDG_RUNTIME_DIR/$SWAY_WL" 15; then
    ok "wayland socket $SWAY_WL ready"
    XSOCK="/tmp/.X11-unix/X${SWAY_XDISP#:}"
    if wait_socket "$XSOCK" 15; then
        ok "XWayland socket $SWAY_XDISP ready"
        sleep 1
        # WAYLAND_DISPLAY  → library uses Wayland backends (screen/input/window).
        # DISPLAY          → inherited by apps; GDK_BACKEND=x11 forces pluma to XWayland.
        # kwrite uses Wayland natively (no override needed).
        log "running example with WAYLAND_DISPLAY=$SWAY_WL DISPLAY=$SWAY_XDISP GDK_BACKEND=x11"
        run_example "WAYLAND_DISPLAY=$SWAY_WL DISPLAY=$SWAY_XDISP GDK_BACKEND=x11"
        test_cli "WAYLAND_DISPLAY=$SWAY_WL DISPLAY=$SWAY_XDISP"
        if [ "$EXAMPLE_RC" -eq 0 ] && [ "$CLI_RC" -eq 0 ]; then
            ok "example & CLI exited 0"
        else
            fail "example ($EXAMPLE_RC) or CLI ($CLI_RC) failed"
        fi
    else
        fail "XWayland socket $SWAY_XDISP did not appear within 15 s"
        cat /tmp/perfuncted-logs/sway-xwl.log
    fi
else
    fail "wayland socket $SWAY_WL did not appear within 15 s"
    cat /tmp/perfuncted-logs/sway-xwl.log
fi

stop_sway
ok "sway session 2 stopped"
rm -f ${TMPDIR:-/tmp}/perfuncted-kwrite.txt ${TMPDIR:-/tmp}/perfuncted-pluma.txt

# ── SESSION 3: Nested X11 (sway WLR_BACKENDS=x11) ────────────────────────────

echo ""
echo "── SESSION 3: Nested X11 ────────────────────────────────────"
assert_no_test_file

# sway WLR_BACKENDS=x11 renders into an existing X server (e.g. :0).
# It still creates a Wayland socket — we target that.
SWAY_WL="$(next_wayland_display)"
log "starting sway (WLR_BACKENDS=x11) targeting DISPLAY=$DISPLAY → $SWAY_WL"

WLR_BACKENDS=x11 WLR_RENDERER=pixman DISPLAY="$DISPLAY" \
    sway --unsupported-gpu -c config/sway/nested.conf \
    > /tmp/perfuncted-logs/sway-x11.log 2>&1 &
SWAY_PID=$!
log "sway PID=$SWAY_PID"

if wait_socket "$XDG_RUNTIME_DIR/$SWAY_WL" 15; then
    ok "wayland socket $SWAY_WL ready"
    sleep 1
    log "running example with WAYLAND_DISPLAY=$SWAY_WL DISPLAY=$DISPLAY"
    run_example "WAYLAND_DISPLAY=$SWAY_WL DISPLAY=$DISPLAY"
    test_cli "WAYLAND_DISPLAY=$SWAY_WL DISPLAY=$DISPLAY"
    if [ "$EXAMPLE_RC" -eq 0 ] && [ "$CLI_RC" -eq 0 ]; then
        ok "example & CLI exited 0"
    else
        fail "example ($EXAMPLE_RC) or CLI ($CLI_RC) failed"
    fi
else
    fail "wayland socket $SWAY_WL did not appear within 15 s"
    cat /tmp/perfuncted-logs/sway-x11.log
fi

stop_sway
ok "sway session 3 stopped"
rm -f ${TMPDIR:-/tmp}/perfuncted-kwrite.txt ${TMPDIR:-/tmp}/perfuncted-pluma.txt

# ── Summary ───────────────────────────────────────────────────────────────────

echo ""
echo "══════════════════════════════════════════════"
printf "  passed: %d   failed: %d\n" "$PASS" "$FAIL"
echo "══════════════════════════════════════════════"
echo "  logs: /tmp/perfuncted-logs/"
echo ""
[ "$FAIL" -eq 0 ]
