# justfile — dev workflow for github.com/nskaggs/perfuncted
# Run `just` to see available recipes. Requires: just, golangci-lint,
# govulncheck, and deadcode.
#
# CI reads the supported Go line from go.mod and checks this repository out
# without the parent workspace. Local recipes pin the validated patch release
# and disable the workspace overlay for equivalent module resolution.
export GOTOOLCHAIN := "go1.27.4"
export GOWORK := "off"

# Append Go-installed quality tools without overriding existing PATH entries.
export PATH := env_var("PATH") + ":" + env_var("HOME") + "/go/bin"

# Keep CI quality-tool installation reproducible.
golangci_lint_version := "v2.15.0"
vulncheck_version := "v1.7.0"
deadcode_version := "v0.49.0"

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
    # Keep pristine pinned upstream source untouched. Enforce formatting on
    # local source, all new nested tests, and locally modified XGB files.
    @files=$(find . -name '*.go' -not -path './internal/third_party/xgb/*'; \
        printf '%s\n' ./internal/third_party/xgb/xgb.go ./internal/third_party/xgb/conn.go ./internal/third_party/xgb/xproto/xproto.go; \
        find ./internal/third_party/xgb -name '*_test.go'); \
      unformatted=$(printf '%s\n' "$files" | xargs -r gofmt -l); \
      if [ -n "$unformatted" ]; then \
        echo 'FAIL: Files not formatted:'; printf '%s\n' "$unformatted"; \
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
quality: quality-fast lint deadcode vulncheck test-unit coverage-convenience test-release-command-contracts test-module-consumer

# Require full statement coverage in the new scripting facade, rather than
# imposing a spurious 100% requirement on unrelated legacy source.
coverage-convenience:
    @set -eu; profile=$(mktemp); trap 'rm -f "$profile"' EXIT; \
        CGO_ENABLED=0 go test -short -count=1 -coverprofile="$profile" . ./input; \
        CGO_ENABLED=0 go test -count=1 -v ./scripts/check_convenience_coverage -args "$profile"

# Every ledger-listed deterministic command scenario must execute and pass.
test-release-command-contracts:
    CGO_ENABLED=0 go test ./cmd/pf -run '^TestReleaseCommandCoverageInventory$' -count=1 -v
    CGO_ENABLED=0 go run ./scripts/check_release_command_contracts

# Verify a real distributed Go module can be consumed without local replacements.
test-module-consumer:
    CGO_ENABLED=0 go test ./scripts/check_module_consumer -run "^TestPublishedModuleConsumer$" -count=1 -v

# Pre-commit: runs only the fastest essential checks
precommit: quality-fast

# Run every local CI gate with headless display coverage.
ci: test-race benchmark desktop-benchmark quality test-integration-headless-x11 test-input-observation-x11 test-integration-headless-wayland test-session test-integration-backends test-accessibility-certification test-release


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

# Run the benchmark lane used by the remote performance job.
benchmark:
    CGO_ENABLED=0 go test ./find ./accessibility ./input -run '^$' -bench 'Pixel(Hash|Found)|LocateExact|MatchTemplate|CachedChildren|RemoteKeyCode' -benchmem -count=5

# Run bounded real desktop benchmarks. The suite starts its own headless
# Wayland session and records the backend/display/runtime labels in -v output.
# The 50% comparison budget is intentionally broad for shared CI hosts.
desktop-benchmark:
    @set -euo pipefail; \
      output=$(mktemp); \
      trap 'rm -f "$output"' EXIT; \
      PERFUNCTED_CAPTURE_BENCH_REAL=1 CGO_ENABLED=0 go test -tags=desktopbench . -run '^$' -bench '^BenchmarkDesktop(OpenClose|RegionHash|Capture|InputClickType|WindowListActivate|AccessibilitySnapshot|AccessibilityAction)$' -benchmem -benchtime=3x -count=3 -v | tee "$output"; \
      CGO_ENABLED=0 go run ./scripts/check_benchmark_regression -baseline benchmarks/desktop-baseline.txt -current "$output" -max-regression 0.50

# Run unit tests (default alias)
test: test-unit

# Run the shared integration suite in one display mode.
test-integration-suite MODE:
    CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER={{MODE}} go test -tags=integration ./integration -run '^Test(Integration|WindowCaptureIntegration|RetryChangedSnapshot)$' -count=1

# Real public API, release CLI, native desktop clients, and scripted side effects.
# Select only the capability-appropriate visual lane; unsupported operations
# are asserted rather than silently skipped. One suite owns the display.
test-release-matrix MODE:
    @set -eu; case "{{MODE}}" in headless-x11|nested-x11) visual=TestReleaseVisualAcceptanceX11;; headless-wayland|nested-wayland) visual=TestReleaseVisualAcceptanceWayland;; *) echo "unknown release display {{MODE}}" >&2; exit 2;; esac; \
      CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER={{MODE}} go run ./scripts/check_release_command_contracts --live ./integration "$visual"; \
      CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER={{MODE}} go run ./scripts/check_release_command_contracts --live ./integration TestReleaseCLIWorkflowMatrix TestClipboardRoundTrip_Unicode TestClipboardRoundTrip_Whitespace TestClipboardRoundTrip_Newlines TestClipboardRoundTrip_ASCII TestClipboard_NoContamination TestOutputList_ReportsGeometry TestScreenResolution_Sane

# Strict real framebuffer, OCR, pointer and incremental screen stream tests.
test-release-visual-x11:
    CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER=headless-x11 go test -tags=integration ./integration -run '^TestReleaseVisualAcceptanceX11$' -count=1 -v

# Exercise native X11 keyboard/mouse observation, cancellation and shutdown
# using a fresh private Xvfb per test; generic backend tests skip these.
_test-input-observation-x11 TESTNAME:
    CGO_ENABLED=0 PF_X11_OBSERVATION_INTEGRATION=1 go test ./input -run '^TestObservationX11{{TESTNAME}}$' -count=1 -v -timeout=2m

test-input-observation-x11:
    just _test-input-observation-x11 ControlledIntegration
    just _test-input-observation-x11 CrossFocusIntegration
    just _test-input-observation-x11 SessionClose
    just _test-input-observation-x11 CanceledAfterNativePeerVerification
    CGO_ENABLED=0 PF_X11_OBSERVATION_INTEGRATION=1 go test ./input -run '^TestInputStateX11ControlledIntegration$' -count=1 -v -timeout=2m
    CGO_ENABLED=0 PF_X11_OBSERVATION_INTEGRATION=1 go run ./scripts/check_release_command_contracts --live ./cmd/pf TestInputHotkeyCLIControlledX11

# Test the session package lifecycle: creates its own headless session from scratch.
test-session:
    CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER=headless-wayland go test -tags=integration ./integration -run '^TestSessionLifecycle$' -count=1

# Run the shared integration suite against headless X11.
test-integration-headless-x11:
    just test-release-matrix headless-x11
    just test-integration-suite headless-x11

# Run the shared integration suite against nested X11.
test-integration-nested-x11:
    just test-release-matrix nested-x11
    just test-integration-suite nested-x11

# Integration suite against nested X11 with tracing and slower execution
test-integration-nested-x11-debug:
    CGO_ENABLED=0 PF_TRACE_ACTIONS=1 PF_TRACE_DELAY=1000ms PF_TEST_DISPLAY_SERVER=nested-x11 go test -tags=integration ./integration -run '^Test(Integration|WindowCaptureIntegration|RetryChangedSnapshot)$' -count=1 -v

# Run the shared integration suite against headless Wayland.
test-integration-headless-wayland:
    just test-release-matrix headless-wayland
    just test-release-named-sessions
    just test-integration-suite headless-wayland
    CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER=headless-wayland go run ./scripts/check_release_command_contracts --live ./cmd/pf TestWorkflowControlHeadlessWayland

# Verify named session lifecycle with real CLI processes and independent Sway sessions.
test-release-named-sessions:
    CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER=headless-wayland go run ./scripts/check_release_command_contracts --live ./integration TestReleaseNamedSessions

# Strict KDE/Qt + GNOME/GTK AT-SPI certification in headless Wayland.
# Unlike the general integration suite, missing accessibility or application
# support is a certification failure, not an optional capability.
test-accessibility-certification:
    CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER=headless-wayland PF_REQUIRE_ACCESSIBILITY=1 go run ./scripts/check_release_command_contracts --live ./integration TestAccessibilityCertification TestCertificationEditableReadRetryDoesNotAcceptWrongIdentity

# Run the shared integration suite against nested Wayland.
test-integration-nested-wayland:
    just test-release-matrix nested-wayland
    just test-integration-suite nested-wayland

# Integration suite against nested Wayland with tracing and slower execution
test-integration-nested-wayland-debug:
    CGO_ENABLED=0 PF_TRACE_ACTIONS=1 PF_TRACE_DELAY=1000ms PF_TEST_DISPLAY_SERVER=nested-wayland go test -tags=integration ./integration -run '^Test(Integration|WindowCaptureIntegration|RetryChangedSnapshot)$' -count=1 -v

# Run the package-level backend integration tests.
test-integration-backends:
    CGO_ENABLED=0 go test -p 1 -tags=integration ./window ./input ./screen ./clipboard -count=1
    
# Run the general display, session, and backend integration coverage. Strict
# KDE/GTK AT-SPI certification is a separate required CI gate below.
test-integration:
    just test-release-matrix headless-x11
    just test-release-named-sessions
    just test-integration-suite headless-x11
    just test-input-observation-x11
    just test-integration-suite nested-x11
    just test-release-matrix nested-x11
    just test-integration-suite headless-wayland
    just test-release-matrix headless-wayland
    CGO_ENABLED=0 PF_TEST_DISPLAY_SERVER=headless-wayland go run ./scripts/check_release_command_contracts --live ./cmd/pf TestWorkflowControlHeadlessWayland
    just test-integration-suite nested-wayland
    just test-release-matrix nested-wayland
    just test-session
    just test-integration-backends

# Build the Flatpak bundle
build-flatpak:
    flatpak-builder --force-clean --user --install-deps-from=flathub --repo=repo builddir io.github.nskaggs.perfuncted.yml

# Build, install, and validate the Flatpak bundle
test-flatpak:
    CGO_ENABLED=0 go test -tags=integration ./flatpaktest -count=1 -v -timeout=60m

# Run all test suites, including mandatory strict AT-SPI certification.
test-all: test-unit test-session test-integration test-accessibility-certification
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
