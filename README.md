# perfuncted

[![CI](https://github.com/nskaggs/perfuncted/actions/workflows/ci.yml/badge.svg)](https://github.com/nskaggs/perfuncted/actions/workflows/ci.yml)

**perfuncted** is a Go library and CLI for automating Linux desktop applications.
It detects your session type at runtime and selects the right backend automatically —
no configuration needed.

```go
package main

import (
	"context"
	"log"

	"github.com/nskaggs/perfuncted"
)

func main() {
	ctx := context.Background()
	session, err := perfuncted.Open(
		ctx,
		perfuncted.Require(
			perfuncted.CapabilityInput,
			perfuncted.CapabilityWindows,
		),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			log.Print(err)
		}
	}()

	firefox, err := session.Windows.Find(
		ctx,
		perfuncted.WindowMatch{TitleContains: "Firefox"},
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := firefox.Activate(ctx); err != nil {
		log.Fatal(err)
	}
	if err := session.Input.Type(ctx, "hello world"); err != nil {
		log.Fatal(err)
	}
	if err := session.Input.Type(ctx, "{ctrl+s}"); err != nil {
		log.Fatal(err)
	}
}
```

## Backend support

| Session/backend | Screen capture | Input | Window discovery | Window control |
|---|---|---|---|---|
| X11 | XGetImage | XTEST or uinput | EWMH | activate, move, resize, close, minimize, maximize, fullscreen, restore |
| Sway | wlr-screencopy | wl-virtual, XTEST (when `DISPLAY` is set), or uinput | dedicated Sway IPC | activate, move, resize, close, minimize, maximize, fullscreen |
| wlr foreign-toplevel | compositor capture protocol | wl-virtual, XTEST (when `DISPLAY` is set), or uinput | `zwlr_foreign_toplevel_manager_v1` | activate, close, minimize, maximize, restore |
| ext foreign-toplevel | compositor capture protocol or portal | wl-virtual, XTEST (when `DISPLAY` is set), or uinput | `ext_foreign_toplevel_list_v1` | list-only |
| KDE Plasma Wayland | KWin.ScreenShot2, ext capture, or portal | wl-virtual, XTEST (when `DISPLAY` is set), or uinput | KWin D-Bus scripting | activate, move, resize, close, minimize, maximize, restore |
| GNOME Wayland | bundled GNOME bridge, legacy Shell screenshot, or portal | bundled GNOME bridge, then generic fallbacks | bundled GNOME bridge, Shell.Eval compatibility fallback | activate, move, resize, close, minimize, maximize, fullscreen, restore |

`pf info` reports the backend actually opened, failures for unavailable optional
capabilities, and the exact operation list. Discovery does not imply that every
control operation is available.

Wayland portal capture may show a consent dialog. Perfuncted does not implement
portal input: input needs a compositor injection protocol or permission to open
`/dev/uinput`. KDE and non-native GNOME fallback paths may use uinput.

**GNOME (Mutter):** perfuncted carries a small, versioned GNOME Shell
integration and installs it automatically when a GNOME-native capability is
requested. It uses typed Mutter/Clutter/Shell APIs for windows, input, screen
capture, and clipboard; no unsafe mode, portal consent, `wl-clipboard`, or
`/dev/uinput` setup is needed on the native path. GNOME Shell may require one
logout/login after the first installation so it can load the extension. The
older Shell.Eval and screenshot paths remain compatibility fallbacks.
Flatpak can use an already-installed bridge; host extension provisioning from
inside the sandbox is not automatic, so install the native package once when
using the Flatpak. The bundled extension currently declares GNOME Shell 51;
earlier Shell generations remain unclaimed until exercised in a GNOME test
matrix.

## Install

**Flatpak bundle (CI artifact):**

The Flatpak workflow builds `dist/flatpak/perfuncted.flatpak` for tagged
releases and workflow dispatches. Download that artifact before installing it.

```bash
flatpak install --user -y ./dist/flatpak/perfuncted.flatpak
flatpak run io.github.nskaggs.perfuncted --help
```

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
| `wl-clipboard` | Clipboard access on Wayland |
| `xclip` | Clipboard access on X11 |
| `udev` rule or `input` group | `/dev/uinput` access when compositor-scoped or XTEST input is unavailable (see Setup below) |

The selected session also needs its compositor/session services and display
socket available. The integration suite installs additional headless display,
Wayland, X11, D-Bus, and test-application packages in CI.


## Library API

`perfuncted.Open` creates one explicit desktop session. The host desktop is the
default; `WithTarget`, `WithHeadless`, and `WithNested` select other mutually
exclusive targets. Ask only for the capabilities you need with `Require` or
`Optional`.

Every session has non-nil capability facades:

- `session.Screen` captures regions, computes hashes, locates images, and waits for visual changes.
- `session.Input` types, presses keys, clicks, drags, and scrolls.
- `session.Windows` discovers windows and returns stable, session-bound handles for control.
- `session.Outputs` lists displays.
- `session.Clipboard` gets and sets clipboard contents.

Unavailable facade calls return `*perfuncted.CapabilityError`; inspect
`errors.Is(err, perfuncted.ErrUnavailable)` or
`errors.Is(err, perfuncted.ErrUnsupported)` rather than parsing messages.
Supported backend failures are reported as `*perfuncted.OperationError` and
retain their underlying cause for `errors.Is`/`errors.As`. Use
`Session.Launch` for session-owned applications and `Session.Wait` with `All`,
`Any`, `Not`, or `Predicate` for composable waits. `Close` is idempotent and
invalidates child handles. Root session operations reject nil contexts with
`ErrInvalidArgument`; contexts provide cancellation and timeouts.

`DetectSession` returns a typed `SessionDetection` snapshot. It only classifies
the environment; `Open` is the API that establishes and owns a session.

Full API reference: [pkg.go.dev/github.com/nskaggs/perfuncted](https://pkg.go.dev/github.com/nskaggs/perfuncted).
See the generated [CLI reference](docs-cli/pf.md) for `pf` commands.

## Setup

**uinput permission** (needed when the selected input fallbacks reach uinput):

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
just test-integration-headless-x11
just test-integration-headless-wayland
just test-integration-nested-x11
just test-integration-nested-wayland
just test-integration   # all local integration modes
just test-flatpak      # build, install, and validate the Flatpak bundle
```

The integration recipes need the same system packages listed in the CI
workflow. Install `wl-clipboard` for Wayland clipboard round-trip verification
and `xclip` for X11 clipboard round-trip verification.

## Development

Requires Go 1.27.0, [`just`](https://github.com/casey/just), and the tools
installed by `just install-dev-tools`.

```bash
just install-dev-tools
just check       # fmt + vet + lint
just precommit   # fast generation, docs, formatting, and vet checks
just quality     # full static quality suite and unit tests
just docs        # regenerate docs-cli
just pf info     # probe backend availability on the current session
just cleanup-nested # safely reap stale managed sessions after a crash
```

`cleanup-nested` uses the same ownership-aware cleanup as the library. It
retains live session owners and will not signal a recorded child whose
`XDG_RUNTIME_DIR` belongs to a different runtime.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development checks and
[RELEASE.md](RELEASE.md) for the current release workflow.

## License

Apache 2.0 — see [LICENSE](LICENSE).
