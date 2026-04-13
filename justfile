# justfile — dev workflow for github.com/nskaggs/perfuncted
# Run `just` to see available recipes. Requires: just, staticcheck.

default:
    @just --list

# ── quality ────────────────────────────────────────────────────────────────────

# Format all Go source
fmt:
    go fmt ./...

# Vet all packages
vet:
    go vet ./...

# Run staticcheck linter
check: fmt vet
    staticcheck ./...

# Tidy and verify the module graph
tidy:
    go mod tidy
    go mod verify

# Generate CLI documentation
docs:
    rm -rf docs-cli/
    go run ./cmd/pf/ docs --dir ./docs-cli --readme README.md

# Full pre-commit workflow
precommit: check tidy docs

# Build all packages and binaries
build: precommit
    go build ./...

# Build and install the pf CLI to $GOPATH/bin
install: build
    go install ./cmd/pf/

# ── testing ────────────────────────────────────────────────────────────────────

# Run the live integration test against the current display (requires kwrite, pluma, or firefox)
integration:
    go run ./cmd/integration/

# Fast headless Wayland integration test: one isolated sway session + kwrite.
# Verifies screen, input, window, clipboard. Wall time < 2 minutes.
test-headless:
    @bash scripts/test-wayland.sh headless

# Fast nested Wayland integration test: visible sway window on host desktop + kwrite.
# Same coverage as test-headless but in a visible session. Wall time < 2 minutes.
test-nested:
    @bash scripts/test-wayland.sh nested

# Full integration suite: 7 isolated sessions across Wayland, X11, XWayland.
# Slower (several minutes). Use for thorough pre-release validation.
test-full:
    @bash scripts/test-nested.sh

# Run integration suite on the primary desktop (no nested compositor).
# Exercises KWinShot, KWinScriptManager, X11Backend, XTest — backends that
# nested sessions never reach. Moves mouse and types on the real desktop.
test-desktop:
    @bash scripts/test-desktop.sh

# ── dev environment ────────────────────────────────────────────────────────────

# Run the pf CLI
pf *args:
    go run ./cmd/pf/ {{args}}

# Run the pf CLI inside the nested sway session
nested-pf *args:
    WAYLAND_DISPLAY="${SWAY_WAYLAND_DISPLAY:-wayland-1}" go run ./cmd/pf/ {{args}}

# Launch a visible isolated nested sway session (wlroots) connected to the host desktop.
# Creates a temporary XDG_RUNTIME_DIR so host processes do not leak into it.
nested:
    #!/usr/bin/env bash
    set -e
    HOST_XDG="$XDG_RUNTIME_DIR"
    HOST_WL="$WAYLAND_DISPLAY"
    MY_XDG=$(mktemp -d -t perfuncted-xdg-XXXXXX)

    export XDG_RUNTIME_DIR=$MY_XDG
    export WAYLAND_DISPLAY=wayland-1
    echo "============================================="
    echo "Nested Sway session starting..."
    echo "Connect your terminal by running:"
    echo "  export XDG_RUNTIME_DIR=$MY_XDG"
    echo "  export WAYLAND_DISPLAY=$WAYLAND_DISPLAY"
    echo ""
    echo "Or simply use: pf --nested <command>"
    echo ""
    echo "When done, tear down with: just cleanup-nested"
    echo "============================================="

    # Run sway natively inside the isolated XDG directory.
    # We pass the absolute path to the host Wayland socket so it safely connects
    # out to the outer desktop, while creating its own wayland-1 and sway-ipc
    # strictly inside MY_XDG. This fixes Firefox sandboxing and IPC.
    WLR_BACKENDS=wayland WLR_RENDERER=pixman \
    XDG_RUNTIME_DIR="$MY_XDG" WAYLAND_DISPLAY="$HOST_XDG/$HOST_WL" \
    sway --unsupported-gpu -c config/sway/nested.conf &

# ── maintenance ────────────────────────────────────────────────────────────────

# Clean up stale nested session processes and sockets.
# Run this manually if a session crashes without cleaning up after itself.
cleanup-nested:
    @echo "Cleaning up stale nested session processes..."
    @for f in /proc/[0-9]*/environ; do \
        [ -r "$$f" ] || continue; \
        tr '\0' '\n' < "$$f" 2>/dev/null | grep -q '^XDG_RUNTIME_DIR=/tmp/perfuncted-xdg-' || continue; \
        pid=$${f%/environ}; pid=$${pid#/proc/}; \
        kill -9 "$$pid" 2>/dev/null || true; \
    done
    @sleep 1
    @echo "Cleaning up stale temp files and sockets..."
    -for dir in /tmp/perfuncted-xdg-*/gvfs; do [ -d "$$dir" ] && fusermount -u "$$dir" 2>/dev/null || true; done
    -rm -rf /tmp/perfuncted-xdg-* 2>/dev/null || true
    -rm -f /tmp/perfuncted-logs/*.log /tmp/perfuncted-logs/*.res 2>/dev/null || true
    -rm -f /tmp/pf-test-*.png 2>/dev/null || true
    -rm -f /tmp/*-kwrite.txt /tmp/*-pluma.txt 2>/dev/null || true
    -rm -f /tmp/*-firefox-before.png /tmp/*-firefox-after.png 2>/dev/null || true
    @echo "Cleanup complete."
