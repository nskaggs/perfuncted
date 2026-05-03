# justfile — dev workflow for github.com/nskaggs/perfuncted
# Run `just` to see available recipes. Requires: just, staticcheck, govulncheck, deadcode.

default:
    @just --list

# ── quality ────────────────────────────────────────────────────────────────────

# Format all Go source
fmt:
    gofmt -w .

# Vet all packages
vet:
    go vet ./...

# Run staticcheck linter
lint:
    staticcheck ./...

# Check formatting
check-fmt:
    test -z "$(gofmt -l .)"

# Run all quality checks
check: check-fmt vet lint

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

# Install development tools
install-dev-tools:
    go install honnef.co/go/tools/cmd/staticcheck@latest
    go install golang.org/x/vuln/cmd/govulncheck@latest
    go install golang.org/x/tools/cmd/deadcode@latest

# Generate CLI code and documentation
generate:
    go run -tags=gencli ./scripts/gen_cli.go
    rm -rf docs-cli/
    go run ./cmd/pf/ docs --dir ./docs-cli

# Generate CLI documentation
docs: generate

# Verify generated files are current
check-generate:
    just generate
    git diff --exit-code -- cmd/pf/autogen_gen.go docs-cli

# Pre-commit: generated files + all checks + unit tests
precommit: check-generate check test-unit

# Build all packages and binaries
build:
    go build ./...

# Build and install the pf CLI to $GOPATH/bin
install: build
    go install ./cmd/pf/

# ── testing ────────────────────────────────────────────────────────────────────

# Run short (unit) tests only
test-unit:
    go test -short -race ./...

# Run unit tests (default alias)
test: test-unit

# Test the session package lifecycle: creates its own headless session from scratch.
test-session:
    PF_TEST_DISPLAY_SERVER=headless-wayland go test -tags=integration ./integration -run TestSessionLifecycle -count=1

# Run the shared integration suite against headless X11.
test-integration-headless-x11:
    PF_TEST_DISPLAY_SERVER=headless-x11 go test -tags=integration ./integration -count=1

# Run the shared integration suite against nested X11.
test-integration-nested-x11:
    PF_TEST_DISPLAY_SERVER=nested-x11 go test -tags=integration ./integration -count=1

# Run the shared integration suite against headless Wayland.
test-integration-headless-wayland:
    PF_TEST_DISPLAY_SERVER=headless-wayland go test -tags=integration ./integration -count=1

# Run the shared integration suite against nested Wayland.
test-integration-nested-wayland:
    PF_TEST_DISPLAY_SERVER=nested-wayland go test -tags=integration ./integration -count=1

# Backward-compatible aliases.
test-integration-x11: test-integration-headless-x11
test-integration-wayland: test-integration-headless-wayland
test-integration-nested: test-integration-nested-wayland

# Run all integration checks: shared suite plus package-level backend integrations.
test-integration: test-integration-headless-x11 test-integration-headless-wayland
    go test -tags=integration ./window ./input ./screen ./clipboard -count=1

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
