# justfile — dev workflow for github.com/nskaggs/perfuncted
# Run `just` to see available recipes. Requires: just, golangci-lint,
# govulncheck, and deadcode.
#
# CI reads the supported Go line from go.mod and checks this repository out
# without the parent workspace. Local recipes pin the validated patch release
# and disable the workspace overlay for equivalent module resolution.
export GOTOOLCHAIN := "go1.27.0"
export GOWORK := "off"

# Keep CI quality-tool installation reproducible.
golangci_lint_version := "v2.13.2"
vulncheck_version := "v1.7.0"
deadcode_version := "v0.44.0"

default:
    @just --list

# ── quality ────────────────────────────────────────────────────────────────────

# Format all Go source
fmt:
    gofmt -w .

# Vet all packages
vet:
    go vet ./...

# Run golangci-lint
lint:
    golangci-lint run ./...

# Check formatting
check-fmt:
    @if gofmt -l . | grep -q .; then \
        echo "  FAIL: Files not formatted:"; \
        gofmt -l .; \
        echo "  Run 'just fmt' and try again."; \
        exit 1; \
    fi

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
    CGO_ENABLED=0 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{golangci_lint_version}}
    CGO_ENABLED=0 go install golang.org/x/vuln/cmd/govulncheck@{{vulncheck_version}}
    CGO_ENABLED=0 go install golang.org/x/tools/cmd/deadcode@{{deadcode_version}}

# Generate CLI code and documentation
generate:
    CGO_ENABLED=0 go run -tags=gencli ./scripts/gen_cli.go
    rm -rf docs-cli/
    CGO_ENABLED=0 go run ./cmd/pf/ docs --dir ./docs-cli

# Generate CLI documentation
docs: generate

# Verify generated files are current
check-generate:
	@set -e; tmp=$(mktemp -d); trap 'rm -rf "$tmp"' 0 2 3 15; \
	CGO_ENABLED=0 go run -tags=gencli ./scripts/gen_cli.go --output "$tmp/autogen_gen.go"; \
	CGO_ENABLED=0 go run ./cmd/pf/ docs --dir "$tmp/docs-cli" >/dev/null; \
	diff -u cmd/pf/autogen_gen.go "$tmp/autogen_gen.go"; \
	diff -ru docs-cli "$tmp/docs-cli"

# Verify public Go documentation and docs-cli descriptions.
# Fails if any file still says "Auto-generated wrapper" — add a real
# short/long description to scripts/cli-mapping.yaml instead.
check-docs:
    @echo "Checking docs-cli for placeholder text..."
    @if grep -rl "Auto-generated wrapper" docs-cli/ cmd/; then \
        echo "ERROR: found 'Auto-generated wrapper' placeholder text."; \
        echo "  Add a 'short' description in scripts/cli-mapping.yaml for the affected method."; \
        exit 1; \
    fi
    @echo "docs-cli: OK (no placeholders found)"
    golangci-lint run --config .golangci-docs.yml --timeout=5m ./ ./clipboard ./find ./input ./output ./pftest ./poll ./screen ./transport ./window

# Verify CLI commands and API methods are in sync (coverage both directions).
check-api-sync:
    CGO_ENABLED=0 go run ./scripts/verify_cli_api.go


# ── CI & quality ──────────────────────────────────────────────────────────────

# Run essential fast quality checks (formatting, generation, vet)
# Ideal for pre-commit hooks.
quality-fast: check-generate check-docs check-api-sync check-fmt vet

# Run the full static quality suite (includes linters and unit tests)
# This matches the 'quality' job in GitHub Actions.
quality: quality-fast lint deadcode vulncheck test-unit

# Pre-commit: runs only the fastest essential checks
precommit: quality-fast

# Run everything CI does: quality + integration + release smoke tests
# This aggregates all major CI jobs for local reproduction.
ci: quality test-integration test-release


# ── build & install ────────────────────────────────────────────────────────────
build:
    # Produce a repo-root pf binary used by release tests
    CGO_ENABLED=0 go build -o pf ./cmd/pf
    # Also build all packages for parity
    CGO_ENABLED=0 go build ./...
    # Check for dead (unreachable) code
    deadcode -test ./...

# Build and install the pf CLI to $GOPATH/bin
install: build
    CGO_ENABLED=0 go install ./cmd/pf/

# ── testing ────────────────────────────────────────────────────────────────────

# Run short (unit) tests only
test-unit:
    CGO_ENABLED=0 go test -short ./...

# Run unit tests with race detector
test-race:
    CGO_ENABLED=1 go test -race -short -count=1 ./...

# Run unit tests (default alias)
test: test-unit

# Run the shared integration suite in one display mode.
test-integration-suite MODE:
    CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER={{MODE}} go test -tags=integration ./integration -run '^TestIntegration$' -count=1

# Test the session package lifecycle: creates its own headless session from scratch.
test-session:
    CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER=headless-wayland go test -tags=integration ./integration -run '^TestSessionLifecycle$' -count=1

# Run the shared integration suite against headless X11.
test-integration-headless-x11:
    just test-integration-suite headless-x11

# Run the shared integration suite against nested X11.
test-integration-nested-x11:
    just test-integration-suite nested-x11

# Integration suite against nested X11 with tracing and slower execution
test-integration-nested-x11-debug:
    CGO_ENABLED=0 PF_TRACE_ACTIONS=1 PF_TRACE_DELAY=1000ms PF_TEST_DISPLAY_SERVER=nested-x11 go test -tags=integration ./integration -run '^TestIntegration$' -count=1 -v

# Run the shared integration suite against headless Wayland.
test-integration-headless-wayland:
    just test-integration-suite headless-wayland

# Run the shared integration suite against nested Wayland.
test-integration-nested-wayland:
    just test-integration-suite nested-wayland

# Integration suite against nested Wayland with tracing and slower execution
test-integration-nested-wayland-debug:
    CGO_ENABLED=0 PF_TRACE_ACTIONS=1 PF_TRACE_DELAY=1000ms PF_TEST_DISPLAY_SERVER=nested-wayland go test -tags=integration ./integration -run '^TestIntegration$' -count=1 -v

# Run the package-level backend integration tests.
test-integration-backends:
    CGO_ENABLED=0 go test -p 1 -tags=integration ./window ./input ./screen ./clipboard -count=1
    
# Run all integration checks: shared suite across every environment plus session and backend coverage.
test-integration:
    just test-integration-suite headless-x11
    just test-integration-suite nested-x11
    just test-integration-suite headless-wayland
    just test-integration-suite nested-wayland
    just test-session
    just test-integration-backends

# Build the Flatpak bundle
build-flatpak:
    flatpak-builder --force-clean --user --install-deps-from=flathub --repo=repo builddir io.github.nskaggs.perfuncted.yml

# Build, install, and validate the Flatpak bundle
test-flatpak:
    CGO_ENABLED=0 go test -tags=integration ./flatpaktest -count=1 -v -timeout=60m

# Run all test suites: unit + session + integration
test-all: test-unit test-session test-integration
    @echo "Completed test-all"

# ── release smoke tests ────────────────────────────────────────────────────

# Build the pf binary and run the release smoke test (static tests only, no display required).
# Validates the binary exits correctly, version output, help text, and info JSON.
test-release-static: build
    CGO_ENABLED=0 PF_BINARY=./pf go test -tags=release -v -run TestBinaryStatic ./release/ -count=1

# Run the full release smoke test against a headless Wayland session.
# Starts a real sway session and drives the built binary through screen/window/clipboard commands.
test-release: build
    CGO_ENABLED=0 PF_BINARY=./pf PF_TEST_DISPLAY_SERVER=headless-wayland go test -tags=release -v ./release/ -count=1

# Run the release smoke test against an explicit binary (e.g. a GoReleaser dist artifact).
# Usage: just test-release-binary ./dist/pf_linux_amd64/pf
test-release-binary BINARY:
    CGO_ENABLED=0 PF_BINARY={{BINARY}} PF_TEST_DISPLAY_SERVER=headless-wayland go test -tags=release -v ./release/ -count=1

# Run the pf CLI with the given arguments
run *args: build
    CGO_ENABLED=0 go run ./cmd/pf/ {{args}}


# ── dev environment ────────────────────────────────────────────────────────────

# Run the pf CLI
pf *args:
    CGO_ENABLED=0 go run ./cmd/pf/ {{args}}

# Run the pf CLI inside the nested sway session
nested-pf *args:
    CGO_ENABLED=0 WAYLAND_DISPLAY="${SWAY_WAYLAND_DISPLAY:-wayland-1}" go run ./cmd/pf/ {{args}}

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
    sway --unsupported-gpu -c configs/nested.conf &

# ── maintenance ────────────────────────────────────────────────────────────────

# Clean up stale nested session processes and sockets.
# Run this manually if a session crashes without cleaning up after itself.
cleanup-nested:
    CGO_ENABLED=0 go run ./cmd/pf session cleanup --max-age 24h
