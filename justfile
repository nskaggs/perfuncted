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

# Full pre-commit workflow
precommit: check tidy

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

# Launch a nested sway session (wlroots) inside the current KDE/Wayland desktop.
# Sway opens as a window; automation targets WAYLAND_DISPLAY=wayland-1.
nested:
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
test-nested:
    @bash scripts/test-nested.sh

# Run integration suite on the primary desktop (no nested compositor).
# Exercises KWinShot, KWinScriptManager, X11Backend, XTest — backends that
# nested sessions never reach.  Moves mouse and types on the real desktop.
test-desktop:
    @bash scripts/test-desktop.sh
