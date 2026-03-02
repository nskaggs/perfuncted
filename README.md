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

```
pf info                                    # show detected session and backend availability

pf screen grab     --rect 0,0,1920,1080 --out shot.png
pf screen checksum --rect 100,100,200,200
pf screen pixel    --x 960 --y 540

pf input move      --x 500 --y 300
pf input click     --x 500 --y 300 --button 1
pf input type      "hello world"
pf input key       ctrl+s

pf window list
pf window activate "Firefox"
pf window active
pf window move     --title "Firefox" --x 100 --y 100
pf window resize   --title "Firefox" --w 800 --h 600
```

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
bash scripts/test-nested.sh   # Wayland → XWayland → X11
```

Optional: install `wl-clipboard` to enable clipboard round-trip verification.

## Development

Requires: [`just`](https://github.com/casey/just), [`staticcheck`](https://staticcheck.io).

```bash
just check     # fmt + vet + staticcheck
just build     # full pre-commit check then build
just pf info   # probe backend availability on the current session
```

## License

Apache 2.0 — see [LICENSE](LICENSE).
