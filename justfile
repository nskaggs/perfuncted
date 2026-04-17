# justfile — dev workflow for github.com/nskaggs/perfuncted
# Run `just` to see available recipes. Requires: just, staticcheck, govulncheck, deadcode.
# Install dev tools: go install staticcheck.io/staticcheck@latest
#                    go install golang.org/x/vuln/cmd/govulncheck@latest
#                    go install golang.org/x/tools/cmd/deadcode@latest

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

# Check for dead (unreachable) code
deadcode:
    deadcode -test ./...

# Check dependencies for known vulnerabilities
vulncheck:
    govulncheck ./...

# Tidy and verify the module graph
tidy:
    go mod tidy
    go mod verify

# Generate CLI documentation
docs:
    rm -rf docs-cli/
    go run ./cmd/pf/ docs --dir ./docs-cli

# Full pre-commit workflow
precommit: check deadcode tidy docs vulncheck

# Build all packages and binaries
build: precommit
    go build ./...

# Build and install the pf CLI to $GOPATH/bin
install: build
    go install ./cmd/pf/

# ── testing ────────────────────────────────────────────────────────────────────

# Run unit tests (with race detector)
test-unit:
    go test -race ./...

# Test the session package lifecycle: creates its own headless session from scratch.
test-session:
    @bash scripts/test-session.sh

# Run integration tests (defaults to headless if no arg provided)
# Usage: just test-integration            -> headless
#        just test-integration desktop    -> desktop
#        just test-integration nested     -> nested
test-integration *args:
    @mode="{{args}}"; \
    if [ -z "$$mode" ]; then mode=headless; fi; \
    bash scripts/test-integration.sh "$$mode"

# Run all test suites: unit + session + integration
test-all: test-unit test-session test-integration
    @echo "Completed test-all"

# Run the pf CLI with the given arguments
run *args: build
    go run ./cmd/pf/ {{args}}


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
