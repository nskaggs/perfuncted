#!/usr/bin/env bash
# scripts/test-nested.sh — nested integration test suite
#
# Runs cmd/integration against 6 isolated sessions concurrently:
#   x11-kwrite, x11-pluma              (pure X11 with Openbox)
#   wlroots-wayland-kwrite, wlroots-wayland-pluma  (Sway headless, Wayland apps)
#   wlroots-xwayland-kwrite, wlroots-xwayland-pluma (Sway headless + XWayland, X11 apps)

set -euo pipefail
cd "$(dirname "$0")/.."

# ── cleanup helpers ───────────────────────────────────────────────────────────

# cleanup_stale: remove stale processes, sockets, and temp files from previous runs
cleanup_stale() {
    echo "Cleaning up stale processes from previous runs..."
    # Kill any stale test processes
    pkill -9 -f "dbus-daemon.*perfuncted-xdg" 2>/dev/null || true
    pkill -9 -f "gvfsd-fuse.*perfuncted-xdg" 2>/dev/null || true
    pkill -9 fusermount3 2>/dev/null || true
    pkill -9 sway 2>/dev/null || true
    pkill -9 swaybg 2>/dev/null || true
    pkill -9 openbox 2>/dev/null || true
    pkill -9 Xvfb 2>/dev/null || true
    pkill -9 kwrite 2>/dev/null || true
    pkill -9 pluma 2>/dev/null || true
    sleep 1
    
    # Unmount any remaining gvfs mounts
    for dir in /tmp/perfuncted-xdg-*/gvfs; do
        [ -d "$dir" ] && fusermount -u "$dir" 2>/dev/null || true
    done
    
    # Remove stale temp directories and sockets
    echo "Cleaning up stale temp files and sockets..."
    rm -rf /tmp/perfuncted-xdg-* 2>/dev/null || true
    rm -f /tmp/.X11-unix/X[0-9]* 2>/dev/null || true
    rm -f /tmp/perfuncted-logs/*.log /tmp/perfuncted-logs/*.res 2>/dev/null || true
    rm -f /tmp/pf-test-*.png 2>/dev/null || true
    rm -f /tmp/*-kwrite.txt /tmp/*-pluma.txt 2>/dev/null || true
}

# cleanup_session: clean up after a single test session
cleanup_session() {
    local prefix="$1"
    # Kill any app processes with this prefix
    pkill -TERM -f "kwrite.*${prefix}" 2>/dev/null || true
    pkill -TERM -f "pluma.*${prefix}" 2>/dev/null || true
}

# cleanup_all: final cleanup after all tests complete
cleanup_all() {
    echo ""
    echo "Cleaning up after test suite..."
    pkill -9 -f "dbus-daemon.*perfuncted-xdg" 2>/dev/null || true
    pkill -9 -f "gvfsd-fuse.*perfuncted-xdg" 2>/dev/null || true
    pkill -9 fusermount3 2>/dev/null || true
    sleep 1
    
    # Unmount gvfs
    for dir in /tmp/perfuncted-xdg-*/gvfs; do
        [ -d "$dir" ] && fusermount -u "$dir" 2>/dev/null || true
    done
    
    # Remove temp directories
    rm -rf /tmp/perfuncted-xdg-* 2>/dev/null || true
    rm -f /tmp/.X11-unix/X[0-9]* 2>/dev/null || true
}

# ── environment sanity check ──────────────────────────────────────────────────

check_deps() {
    local missing=()
    for cmd in sway openbox Xvfb xdpyinfo dbus-send wmctrl; do
        if ! command -v "$cmd" &>/dev/null; then
            missing+=("$cmd")
        fi
    done
    
    if [ ${#missing[@]} -gt 0 ]; then
        echo "ERROR: Missing required dependencies: ${missing[*]}"
        echo "Install with: sudo apt-get install sway openbox xvfb x11-utils dbus wmctrl"
        exit 1
    fi

    # Optional but highly recommended for full verification
    local opt_missing=()
    for cmd in wl-copy wl-paste xdotool; do
        if ! command -v "$cmd" &>/dev/null; then
            opt_missing+=("$cmd")
        fi
    done
    if [ ${#opt_missing[@]} -gt 0 ]; then
        echo "WARNING: Optional dependencies missing: ${opt_missing[*]}"
        echo "Clipboard and focus-injection tests may be limited."
    fi
}

# Apply CI-optimized inotify limits for stability in many concurrent sessions
if [ "$(id -u)" -eq 0 ]; then
    sysctl -w fs.inotify.max_user_watches=524288 || true
    sysctl -w fs.inotify.max_user_instances=1024 || true
    sysctl -w fs.inotify.max_queued_events=32768 || true
else
    # Non-root: only attempt via sudo if available and non-interactive
    sudo -n sysctl -w fs.inotify.max_user_watches=524288 2>/dev/null || true
    sudo -n sysctl -w fs.inotify.max_user_instances=1024 2>/dev/null || true
    sudo -n sysctl -w fs.inotify.max_queued_events=32768 2>/dev/null || true
fi

check_deps

echo ""
echo "══════════════════════════════════════════════"
echo " perfuncted nested integration test suite"
echo "══════════════════════════════════════════════"
echo ""
echo "  host WAYLAND_DISPLAY=$WAYLAND_DISPLAY"
echo "  host DISPLAY=$DISPLAY"
echo "  host XDG_RUNTIME_DIR=$XDG_RUNTIME_DIR"

if [ -z "$WAYLAND_DISPLAY" ] || [ ! -S "$XDG_RUNTIME_DIR/$WAYLAND_DISPLAY" ]; then
    echo "FATAL: host Wayland socket not found — cannot continue"
    exit 1
fi

# Clean up stale processes and files before starting
cleanup_stale

HOST_WL="$WAYLAND_DISPLAY"
mkdir -p /tmp/perfuncted-logs

echo "  building integration binary for tests..."
go build -o /tmp/perfuncted-integration ./cmd/integration/

echo "  building pf CLI binary for tests..."
go build -o /tmp/pf-bin ./cmd/pf/

# ── shared helpers ────────────────────────────────────────────────────────────

wait_for_xvfb() {
    local display=$1
    local timeout=10
    local elapsed=0
    
    while ! DISPLAY=$display xdpyinfo >/dev/null 2>&1; do
        sleep 0.2
        elapsed=$((elapsed + 1))
        if [ $elapsed -ge $((timeout * 5)) ]; then
            echo "Xvfb on $display never became ready"
            return 1
        fi
    done
}

wait_for_dbus() {
    local addr=$1
    local timeout=10
    local elapsed=0
    
    while ! DBUS_SESSION_BUS_ADDRESS=$addr dbus-send \
        --session --dest=org.freedesktop.DBus \
        --type=method_call --print-reply \
        /org/freedesktop/DBus org.freedesktop.DBus.ListNames \
        >/dev/null 2>&1; do
        sleep 0.2
        elapsed=$((elapsed + 1))
        if [ $elapsed -ge $((timeout * 5)) ]; then
            echo "D-Bus never became ready"
            return 1
        fi
    done
}

launch_with_retry() {
    local env_str="$1"
    local app="$2"
    local max_attempts=3
    
    for attempt in $(seq 1 $max_attempts); do
        log "launching app=$app (attempt $attempt)..."
        # shellcheck disable=SC2086
        eval "clean_run $env_str /tmp/perfuncted-integration --app $app 2>&1" && return 0
        
        echo "Attempt $attempt failed, retrying..."
        cleanup_session "$PF_TEST_PREFIX"
        sleep 2
    done
    
    return 1
}

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

# wait_new_x11_socket BEFORE_SNAPSHOT TIMEOUT_SECS
# Polls until a new /tmp/.X11-unix/X* socket appears that was not in BEFORE_SNAPSHOT.
# Prints the display string (e.g. ":1") on success.
wait_new_x11_socket() {
    local before="$1" secs="$2" i=0
    while [ $i -lt $((secs*10)) ]; do
        for f in /tmp/.X11-unix/X*; do
            [ -S "$f" ] || continue
            local base; base=$(basename "$f")
            echo "$before" | grep -qx "$base" && continue
            echo ":${base#X}"
            return 0
        done
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
    local env_str="$1" app="$2"
    EXAMPLE_RC=0
    log "running app=$app with ENV_STR='$env_str'"
    launch_with_retry "$env_str" "$app" || EXAMPLE_RC=$?
    # Kill any stray app processes spawned by this session
    cleanup_session "$PF_TEST_PREFIX"
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

# ── session worker ────────────────────────────────────────────────────────────
# run_session SESSION_TYPE APP [XVFB_DISP] [PREFIX_OVERRIDE]
#   SESSION_TYPE   : x11 | wlroots-wayland | wlroots-xwayland
#   APP            : kwrite | pluma
#   XVFB_DISP      : X display for Xvfb (e.g. :10), required for x11 and wlroots-xwayland
#   PREFIX_OVERRIDE: Optional prefix for log files and aggregation

run_session() {
    local SESSION_TYPE="$1" APP="$2" XVFB_DISP="${3:-}" PREFIX_OVERRIDE="${4:-}"
    local PREFIX="${SESSION_TYPE}-${APP}"
    [ -n "$PREFIX_OVERRIDE" ] && PREFIX="$PREFIX_OVERRIDE"
    local PASS=0 FAIL=0 SWAY_WL="wayland-1"
    local SWAY_PID="" WLC_PID="" XVFB_PID="" OPENBOX_PID=""
    export PF_TEST_PREFIX="$PREFIX"

    local MY_XDG
    MY_XDG=$(mktemp -d -p /tmp perfuncted-xdg-${PREFIX}-XXXXXX)
    chmod 0700 "$MY_XDG"
    export XDG_RUNTIME_DIR="$MY_XDG"
    export DBUS_SESSION_BUS_ADDRESS="unix:path=$MY_XDG/bus"

    log()  { echo "[$PREFIX] $*"; }
    ok()   { echo "[$PREFIX] ✓ $*"; PASS=$((PASS+1)); }
    fail() { echo "[$PREFIX] ✗ $*"; FAIL=$((FAIL+1)); }

    clean_run dbus-daemon --session --address="$DBUS_SESSION_BUS_ADDRESS" --nofork --print-address &
    DBUS_PID=$!
    if ! wait_for_dbus "$DBUS_SESSION_BUS_ADDRESS"; then
        fail "DBus did not become ready"
        return
    fi

    # Snapshot X11 sockets before starting any compositor (used for XWayland display detection).
    local X11_BEFORE
    X11_BEFORE=$(ls /tmp/.X11-unix/ 2>/dev/null || true)

    case "$SESSION_TYPE" in
        x11)
            # X11 session: run Xvfb + Openbox for EWMH window management
            # This tests pure X11 code paths (XGetImage, XTEST, EWMH) without Wayland
            # Openbox provides full EWMH support including _NET_WM_MOVERESIZE
            log "starting Xvfb $XVFB_DISP"
            clean_run bash -c "Xvfb $XVFB_DISP -screen 0 1024x768x24 > /tmp/perfuncted-logs/xvfb-$PREFIX.log 2>&1" &
            XVFB_PID=$!
            if ! wait_for_xvfb "$XVFB_DISP"; then
                fail "Xvfb did not become ready"
                return
            fi
            # Openbox fully implements _NET_WM_MOVERESIZE for deterministic behavior
            log "starting openbox (DISPLAY=$XVFB_DISP)"
            clean_run DISPLAY="$XVFB_DISP" \
                bash -c "openbox --config-file config/openbox/rc.xml > /tmp/perfuncted-logs/openbox-$PREFIX.log 2>&1" &
            OPENBOX_PID=$!
            sleep 2
            ;;
        wlroots-wayland)
            # Sway headless: full wlroots Wayland compositor with deterministic output size
            log "starting sway (WLR_BACKENDS=headless) → $SWAY_WL"
            clean_run WLR_BACKENDS=headless WLR_RENDERER=pixman \
                bash -c "sway --unsupported-gpu -c config/sway/ci.conf > /tmp/perfuncted-logs/sway-$PREFIX.log 2>&1" &
            SWAY_PID=$!
            ;;
        wlroots-xwayland)
            # Sway headless + XWayland: wlroots compositor with X11 compatibility
            log "starting sway (WLR_BACKENDS=headless + XWayland) → $SWAY_WL"
            clean_run WLR_BACKENDS=headless WLR_RENDERER=pixman XWAYLAND_NO_AUTH=1 \
                bash -c "sway --unsupported-gpu -c config/sway/nested.conf > /tmp/perfuncted-logs/sway-$PREFIX.log 2>&1" &
            SWAY_PID=$!
            ;;
    esac

    # X11 sessions don't have a Wayland socket - proceed directly
    if [ "$SESSION_TYPE" = "x11" ]; then
        # Pure X11 environment: force both GTK and Qt to use X11 backend
        local ENV_STR="DISPLAY=$XVFB_DISP GDK_BACKEND=x11 QT_QPA_PLATFORM=xcb"
        
        sleep 1
        run_example "$ENV_STR" "$APP"
        test_cli "$ENV_STR" "$PREFIX"
        pkill -TERM -f "${APP}.*$PF_TEST_PREFIX" 2>/dev/null || true
        if [ "$EXAMPLE_RC" -eq 0 ] && [ "$CLI_RC" -eq 0 ]; then
            ok "example & CLI exited 0"
        else
            fail "example ($EXAMPLE_RC) or CLI ($CLI_RC) failed"
        fi
    elif wait_socket "$MY_XDG/$SWAY_WL" 60; then
        export WAYLAND_DISPLAY="$SWAY_WL"

        # Determine the test environment string based on session type.
        local ENV_STR="" SKIP=0
        case "$SESSION_TYPE" in
            wlroots-wayland)
                # Full Wayland: both GTK and Qt use Wayland backend
                ENV_STR="WAYLAND_DISPLAY=$SWAY_WL DISPLAY= GDK_BACKEND=wayland QT_QPA_PLATFORM=wayland"
                ;;
            wlroots-xwayland)
                # XWayland: force both GTK and Qt to use X11 backend
                local XDISP
                XDISP=$(wait_new_x11_socket "$X11_BEFORE" 60) || { fail "XWayland socket did not appear within 60 s"; SKIP=1; }
                if [ "$SKIP" -eq 0 ]; then
                    ENV_STR="WAYLAND_DISPLAY=$SWAY_WL DISPLAY=$XDISP GDK_BACKEND=x11 QT_QPA_PLATFORM=xcb"
                fi
                ;;
        esac

        if [ "$SKIP" -eq 0 ]; then
            clean_run WAYLAND_DISPLAY="$SWAY_WL" wl-paste --watch cat >/dev/null 2>&1 &
            WLC_PID=$!
            sleep 2
            run_example "$ENV_STR" "$APP"
            test_cli "$ENV_STR" "$PREFIX"
            pkill -TERM -f "${APP}.*$PF_TEST_PREFIX" 2>/dev/null || true
            if [ "$EXAMPLE_RC" -eq 0 ] && [ "$CLI_RC" -eq 0 ]; then
                ok "example & CLI exited 0"
            else
                fail "example ($EXAMPLE_RC) or CLI ($CLI_RC) failed"
            fi
        fi
    else
        fail "wayland socket $SWAY_WL did not appear within 60 s"
        ls -la "$MY_XDG" >> /tmp/perfuncted-logs/sway-$PREFIX.log
    fi

    [ -n "$SWAY_PID"    ] && kill "$SWAY_PID"    2>/dev/null || true
    [ -n "$WLC_PID"     ] && kill "$WLC_PID"     2>/dev/null || true
    [ -n "$XVFB_PID"    ] && kill "$XVFB_PID"    2>/dev/null || true
    [ -n "$OPENBOX_PID" ] && kill "$OPENBOX_PID" 2>/dev/null || true
    kill "$DBUS_PID" 2>/dev/null || true
    
    # Wait for sockets to disappear
    if [ "$SESSION_TYPE" = "x11" ]; then
        wait_no_socket "/tmp/.X11-unix/X${XVFB_DISP#:}" 10 || true
    else
        wait_no_socket "$MY_XDG/$SWAY_WL" 10 || true
    fi
    
    ok "session stopped"
    echo "$PASS $FAIL" > /tmp/perfuncted-logs/$PREFIX.res

    [ -d "$MY_XDG/gvfs" ] && fusermount -u "$MY_XDG/gvfs" 2>/dev/null || true
    rm -rf "$MY_XDG" 2>/dev/null || true
}

# ── execute concurrently ──────────────────────────────────────────────────────

echo "  launching 6 isolated sessions concurrently..."
# X11 sessions: pure Xvfb, no compositor (use displays :10, :11)
( run_session x11              kwrite :10 > /tmp/perfuncted-logs/x11-kwrite-test.log     2>&1 ) &
( run_session x11              pluma  :11 > /tmp/perfuncted-logs/x11-pluma-test.log      2>&1 ) &
# wlroots-wayland sessions: Sway headless with Wayland apps
( run_session wlroots-wayland  kwrite     > /tmp/perfuncted-logs/wlroots-wayland-kwrite-test.log  2>&1 ) &
( run_session wlroots-wayland  pluma      > /tmp/perfuncted-logs/wlroots-wayland-pluma-test.log   2>&1 ) &
# wlroots-xwayland sessions: Sway headless + XWayland with X11 apps (run sequentially to avoid X display races)
(
    run_session wlroots-xwayland kwrite > /tmp/perfuncted-logs/wlroots-xwayland-kwrite-test.log 2>&1
    run_session wlroots-xwayland pluma  > /tmp/perfuncted-logs/wlroots-xwayland-pluma-test.log  2>&1
) &

wait

# ── aggregate results ─────────────────────────────────────────────────────────

TOTAL_PASS=0
TOTAL_FAIL=0

echo ""
for key in x11-kwrite x11-pluma wlroots-wayland-kwrite wlroots-wayland-pluma wlroots-xwayland-kwrite wlroots-xwayland-pluma; do
    echo "── SESSION: $key ────────────────────────────────────────"
    cat /tmp/perfuncted-logs/${key}-test.log
    if [ -f /tmp/perfuncted-logs/${key}.res ]; then
        read -r p f < /tmp/perfuncted-logs/${key}.res
        TOTAL_PASS=$((TOTAL_PASS + p))
        TOTAL_FAIL=$((TOTAL_FAIL + f))
    else
        echo "  [$key] ✗ FATAL: session crashed without writing results"
        TOTAL_FAIL=$((TOTAL_FAIL + 1))
    fi
    echo ""
done

echo "══════════════════════════════════════════════"
printf "  passed: %d   failed: %d\n" "$TOTAL_PASS" "$TOTAL_FAIL"
echo "══════════════════════════════════════════════"
echo "  logs: /tmp/perfuncted-logs/"

# Final cleanup
cleanup_all

[ "$TOTAL_FAIL" -eq 0 ]
