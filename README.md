# perfuncted

[![CI](https://github.com/nskaggs/perfuncted/actions/workflows/ci.yml/badge.svg)](https://github.com/nskaggs/perfuncted/actions/workflows/ci.yml)

**perfuncted** is a Go library and CLI for automating Linux desktop applications.
It detects your session type at runtime and selects the right backend automatically —
no configuration needed.

```go
pf, _ := perfuncted.New(perfuncted.Options{})
defer pf.Close()

pf.Window.Activate("Firefox")
pf.Input.Type("hello world")
pf.Input.KeyTap("ctrl+s")
```

## Backend support

| Session | Screen capture | Input | Window management |
|---|---|---|---|
| X11 | XGetImage ✅ | XTEST / uinput ✅ | EWMH ✅ |
| wlroots Wayland (Sway, Hyprland) | wlr-screencopy ✅ | wl-virtual / uinput ✅ | Sway IPC / wlr-foreign-toplevel ✅ |
| KDE Plasma Wayland | KWin.ScreenShot2 ✅ | uinput ✅ | KWin D-Bus scripting ✅ |
| GNOME Wayland | xdg-desktop-portal ⚠️ | uinput ✅ | Shell.Eval ⚠️ |

> **KDE:** wl-virtual input protocols are wlroots-only; KDE uses uinput.
>
> **GNOME:** Screen capture shows a one-time consent dialog. Window management
> via `org.gnome.Shell.Eval` works but is disabled by default on GNOME 41+
> (requires unsafe-mode). For full automation, run inside a nested
> [sway](https://swaywm.org/) session.

Run `pf info` to see exactly which backends are active on your system.

## Install

**CLI:**

```bash
go install github.com/nskaggs/perfuncted/cmd/pf@latest
```

**Library:**

```bash
go get github.com/nskaggs/perfuncted
```

**Runtime dependencies** (only install what your session needs):

| Dependency | Required for |
|---|---|
| `udev` rule or `input` group | `/dev/uinput` access (see Setup below) |

## CLI usage

Run `pf help` or `pf [command] --help` for full usage. Quick reference:

<!-- pf-cli-start -->
```
pf clipboard get                        # Print clipboard contents
pf clipboard set                        # Set clipboard contents

pf find color                           # Find the first pixel matching a colour within tolerance
pf find locate                          # Find a reference PNG image within a screen region
pf find scan-for                        # Scan multiple regions until one matches its expected hash
pf find wait-for                        # Wait until a region's pixel hash equals the provided hash
pf find wait-for-change                 # Wait until a region's pixel hash changes from an initial value
pf find wait-for-no-change              # Wait until a region's pixel hash is stable for N consecutive samples
pf find wait-locate                     # Poll until a reference image is found in the search area
pf info                                 # Probe and display supported backends for this environment

pf input click                          # Click a mouse button at coordinates
pf input click-center                   # Click the center of a rectangle
pf input double-click                   # Double-click at coordinates
pf input drag-and-drop                  # Drag from one coordinate to another (press, move, release)
pf input key                            # Send a key or key combination (e.g. ctrl+s, return, escape)
pf input keydown                        # Press and hold a key
pf input keyup                          # Release a held key
pf input mousedown                      # Press a mouse button (optional coords)
pf input mouseup                        # Release a mouse button (optional coords)
pf input move                           # Move mouse to absolute coordinates
pf input scroll                         # Scroll the mouse wheel
pf input type                           # Type a string as keyboard events

pf screen grab                          # Capture a screen region and save as PNG
pf screen hash                          # Print the CRC32 pixel hash of a screen region
pf screen pixel                         # Print the RGB colour of a single pixel
pf screen resolution                    # Print the screen resolution
pf screen watch                         # Continuously print hash changes in a screen region

pf session check                        # Check if the current runtime environment is ready for automation
pf session start                        # Start a headless sway session and print env vars
pf session type                         # Print whether the current session is nested or host

pf window activate                      # Bring a window to the foreground by title substring (case-insensitive)
pf window active                        # Print the title of the currently focused window
pf window close                         # Close a window by title
pf window list                          # List all visible windows
pf window maximize                      # Maximize a window by title
pf window minimize                      # Minimize a window by title
pf window move                          # Move a window to absolute screen coordinates
pf window resize                        # Resize a window
```
<!-- pf-cli-end -->

## Library API

Three top-level bundles are available after `perfuncted.New(...)`:

- **`pf.Screen`** — capture regions, compute pixel hashes, locate images, wait for visual changes
- **`pf.Input`** — type text, tap keys, click and drag, scroll
- **`pf.Window`** — list, activate, resize, move, and wait for windows

Full API reference: [pkg.go.dev/github.com/nskaggs/perfuncted](https://pkg.go.dev/github.com/nskaggs/perfuncted)

## Setup

**uinput permission** (required for input on all Wayland sessions and as X11 fallback):

```bash
echo 'KERNEL=="uinput", GROUP="input", MODE="0660"' | \
  sudo tee /etc/udev/rules.d/99-uinput.rules
sudo udevadm control --reload && sudo udevadm trigger
sudo usermod -aG input $USER   # log out and back in
```

## Testing

The integration suite runs in isolated nested Wayland/X11 sessions and never
touches your real desktop:

```bash
just test-nested
```

Optional: install `wl-clipboard` to enable clipboard round-trip verification.

## Development

Requires: [`just`](https://github.com/casey/just), [`staticcheck`](https://staticcheck.io).

```bash
just check       # fmt + vet + staticcheck
just precommit   # full pre-commit gate (format, vet, lint, tidy, docs)
just pf info     # probe backend availability on the current session
```

## License

Apache 2.0 — see [LICENSE](LICENSE).
