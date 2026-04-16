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
| GNOME Wayland | xdg-desktop-portal ⚠️ | uinput ✅ | Shell.Eval ⚠️ (foreign-toplevel typically restricted) |

> **KDE:** wl-virtual input protocols are wlroots-only; KDE uses uinput.
>
> **GNOME (Mutter):** The compositor intentionally restricts some window-management protocols. There are two paths:
>
> 1. GnomeManager (org.gnome.Shell.Eval): when available it runs JavaScript inside gnome-shell and supports List, Activate, Move, Resize, ActiveTitle, Close, Minimize, Maximize. On many distributions org.gnome.Shell.Eval is disabled by default (GNOME 41+); enabling it requires unsafe-mode and is a security risk.
>
> 2. foreign-toplevel protocol (zwlr_foreign_toplevel_manager_v1 / ext_foreign_toplevel_list_v1): if the compositor advertises this protocol the Wayland backend can use it to list windows and request actions (activate, close, minimize, maximize); however Mutter typically does not advertise it and may ignore requests even when present.
>
> Therefore, on GNOME you will usually rely on org.gnome.Shell.Eval (if enabled) or use a nested wlroots compositor (e.g., nested sway) for full automation.

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
