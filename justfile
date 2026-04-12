# justfile — dev workflow for github.com/nskaggs/perfuncted
# Run `just` to see available recipes. Requires: just, staticcheck.

default:
    @just --list

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
    go run ./cmd/pf/ docs --dir ./docs-cli

# Full pre-commit workflow
precommit: check tidy docs

# Build all packages and binaries
build: precommit
    go build ./...

# Run the live integration test against the current display (requires kwrite)
integration:
    go run ./cmd/integration/

# Run the pf CLI
pf *args:
    go run ./cmd/pf/ {{args}}

# Build and install the pf CLI to $GOPATH/bin
install: build
    go install ./cmd/pf/

# Launch a true isolated nested sway session (wlroots) for testing.
# Creates a temporary XDG_RUNTIME_DIR so host processes do not leak into it.
nested:
    #!/usr/bin/env bash
    set -e
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
    WLR_BACKENDS=wayland WLR_RENDERER=pixman \
      sway --unsupported-gpu -c config/sway/nested.conf &

# Run cmd/integration inside the nested sway session.
nested-example:
    WAYLAND_DISPLAY="${SWAY_WAYLAND_DISPLAY:-wayland-1}" go run ./cmd/integration/

# Run the pf CLI inside the nested sway session.
nested-pf *args:
    WAYLAND_DISPLAY="${SWAY_WAYLAND_DISPLAY:-wayland-1}" go run ./cmd/pf/ {{args}}

# Run the full nested integration test suite: Wayland → XWayland → X11.
# Each session is started fresh and fully torn down before the next.
# The script automatically cleans up stale processes before and after running.
test-nested:
    @bash scripts/test-nested.sh

# Clean up stale nested session processes and sockets.
# Run this manually if you need to clean up without running tests.
cleanup-nested:
    @echo "Cleaning up stale nested session processes..."
    -pkill -9 -f "dbus-daemon.*perfuncted-xdg" 2>/dev/null || true
    -pkill -9 -f "gvfsd-fuse.*perfuncted-xdg" 2>/dev/null || true
    -pkill -9 fusermount3 2>/dev/null || true
    -pkill -9 sway 2>/dev/null || true
    -pkill -9 swaybg 2>/dev/null || true
    -pkill -9 openbox 2>/dev/null || true
    -pkill -9 Xvfb 2>/dev/null || true
    -pkill -9 kwrite 2>/dev/null || true
    -pkill -9 pluma 2>/dev/null || true
    @sleep 1
    @echo "Cleaning up stale temp files and sockets..."
    -for dir in /tmp/perfuncted-xdg-*/gvfs; do [ -d "$$dir" ] && fusermount -u "$$dir" 2>/dev/null || true; done
    -rm -rf /tmp/perfuncted-xdg-* 2>/dev/null || true
    -rm -f /tmp/.X11-unix/X[0-9]* 2>/dev/null || true
    -rm -f /tmp/perfuncted-logs/*.log /tmp/perfuncted-logs/*.res 2>/dev/null || true
    -rm -f /tmp/pf-test-*.png 2>/dev/null || true
    -rm -f /tmp/*-kwrite.txt /tmp/*-pluma.txt 2>/dev/null || true
    @echo "Cleanup complete."

# Run integration suite on the primary desktop (no nested compositor).
# Exercises KWinShot, KWinScriptManager, X11Backend, XTest — backends that
# nested sessions never reach.  Moves mouse and types on the real desktop.
test-desktop:
    @bash scripts/test-desktop.sh
