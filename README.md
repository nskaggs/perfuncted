# perfuncted

[![CI](https://github.com/nskaggs/perfuncted/actions/workflows/ci.yml/badge.svg)](https://github.com/nskaggs/perfuncted/actions/workflows/ci.yml)

**perfuncted** is a Go library and CLI for automating Linux desktop applications.
It detects your session type at runtime and selects the right backend automatically —
no configuration needed.

```go
pf, _ := perfuncted.New(perfuncted.Options{})
defer pf.Close()

pf.Input.Type("hello world")
pf.Window.Activate("Firefox")
img, _ := pf.Screen.Grab(image.Rect(0, 0, 1920, 1080))
```

## Backend support

| Session | Screen capture | Input | Window management |
|---|---|---|---|
| X11 | XGetImage ✅ | XTEST / uinput ✅ | EWMH ✅ |
| wlroots Wayland (Sway, Hyprland) | wlr-screencopy ✅ | wl-virtual / uinput ✅ | wlr-foreign-toplevel ✅ |
| KDE Plasma Wayland | KWin.ScreenShot2 ✅ | wl-virtual / uinput ✅ | KWin D-Bus scripting ✅ |
| GNOME Wayland | xdg-desktop-portal ⚠️ | uinput ✅ | ❌ |

> **GNOME:** Screen capture works but shows a one-time consent dialog. Window
> management is unavailable by design. For full automation, run inside a nested
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
pf find last-pixel                      # Print the RGB colour of the bottom-right pixel of a region
pf find locate                          # Find a reference PNG image within a screen region
pf find pixel-hash                      # Print the CRC32 pixel hash of a screen region
pf find scan-for                        # Scan multiple regions until one matches its expected hash
pf find wait-for                        # Wait until a region's pixel hash equals the provided hash
pf find wait-for-change                 # Wait until a region's pixel hash changes from an initial value
pf find wait-for-no-change              # Wait until a region's pixel hash is stable for N consecutive samples
pf find wait-locate                     # Poll until a reference image is found in the search area
pf info                                 # Probe and display supported backends for this environment

pf input click                          # Click a mouse button at coordinates
pf input click-center                   # Click the center of a rectangle
pf input double-click                   # Double-click at coordinates
pf input drag                           # Drag from one coordinate to another (press, move, release)
pf input key                            # Send a key or key combination (e.g. ctrl+s, return, escape)
pf input keydown                        # Press and hold a key
pf input keyup                          # Release a held key
pf input mousedown                      # Press a mouse button (optional coords)
pf input mouseup                        # Release a mouse button (optional coords)
pf input move                           # Move mouse to absolute coordinates
pf input scroll                         # Scroll the mouse wheel
pf input type                           # Type a string as keyboard events

pf screen checksum                      # Print the CRC32 pixel checksum of a screen region
pf screen grab                          # Capture a screen region and save as PNG
pf screen pixel                         # Print the RGB colour of a single pixel
pf screen resolution                    # Print the screen resolution

pf session check                        # Check if the current runtime environment is ready for automation
pf session start                        # Start a headless sway session and print env vars
pf session type                         # Print whether the current session is nested or host

pf window activate                      # Bring a window to the foreground by title substring
pf window activate-by                   # Bring a window to the foreground by title substring (case-insensitive, library-guaranteed)
pf window active                        # Print the title of the currently focused window
pf window close                         # Close a window by title
pf window list                          # List all visible windows
pf window maximize                      # Maximize a window by title
pf window minimize                      # Minimize a window by title
pf window move                          # Move a window to absolute screen coordinates
pf window resize                        # Resize a window
```
<!-- pf-cli-end -->

## Library usage

### Basic automation

```go
package main

import (
    "context"
    "image"
    "log"
    "time"

    "github.com/nskaggs/perfuncted"
    "github.com/nskaggs/perfuncted/find"
)

func main() {
    pf, err := perfuncted.New(perfuncted.Options{})
    if err != nil {
        log.Fatal(err)
    }
    defer pf.Close()

    // Bring a window to focus and interact with it
    if err := pf.Window.Activate("Firefox"); err != nil {
        log.Fatal(err)
    }
    pf.Input.MouseClick(960, 540, 1)
    pf.Input.Type("hello world")
    pf.Input.KeyTap("ctrl+s")
}
```

### Waiting for screen changes

The `find` package provides pixel-hash based polling — useful for waiting until
a UI element appears or a transition completes, without fixed sleeps.

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

region := image.Rect(0, 0, 400, 300)

// Capture the current state
before, _ := find.GrabHash(pf.Screen, region, nil)

// Trigger some action
pf.Input.KeyTap("return")

// Wait until the region changes (e.g. a dialog appeared)
after, err := find.WaitForChange(ctx, pf.Screen, region, before, 50*time.Millisecond, nil)
if err != nil {
    log.Println("timed out waiting for change")
}
_ = after
```

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
