# perfuncted

[![CI](https://github.com/nskaggs/perfuncted/actions/workflows/ci.yml/badge.svg)](https://github.com/nskaggs/perfuncted/actions/workflows/ci.yml)

**Your Linux desktop, now scriptable.**

Perfuncted provides `pf`, a command-line tool for automating Linux desktop
applications. Capture screenshots, control windows, send keyboard and mouse
input, inspect application controls, and build repeatable workflows with scripts.

Works with X11 and supported Wayland desktops, including GNOME and KDE Plasma.

## Install pf

Install the native Linux CLI (x86-64 or ARM64) with one command:

```sh
curl -fsSL https://raw.githubusercontent.com/nskaggs/perfuncted/main/scripts/install.sh | sh
```

The installer downloads the latest stable binary from GitHub Releases, checks
its SHA-256 checksum, and installs `pf` to `~/.local/bin` without sudo or a Go
toolchain. Rerun the same command to upgrade. If `~/.local/bin` is not on your
`PATH`, add it or run `~/.local/bin/pf` directly.

Pin a version or choose another installation directory:

```sh
curl -fsSL https://raw.githubusercontent.com/nskaggs/perfuncted/main/scripts/install.sh | PF_VERSION=v1.3.1 sh
curl -fsSL https://raw.githubusercontent.com/nskaggs/perfuncted/main/scripts/install.sh | PF_INSTALL_DIR="$HOME/bin" sh
```

The [installer source](scripts/install.sh) is available to inspect before
running. Alternatively, download the `linux_amd64` or `linux_arm64` tarball
directly from [GitHub Releases](https://github.com/nskaggs/perfuncted/releases).

Verify the installation with `pf version` and `pf info`.
### One-time KDE Wayland setup

For complete desktop capture, including native file pickers, authorize Perfuncted
for KWin screenshots once. The installer offers this on an interactive KDE
Wayland session. For an unattended installation, explicitly opt in:

```sh
curl -fsSL https://raw.githubusercontent.com/nskaggs/perfuncted/main/scripts/install.sh | PF_KWIN_CAPTURE=yes sh
```

Already installed using Go or a release archive? Register the existing binary:

```sh
curl -fsSL https://raw.githubusercontent.com/nskaggs/perfuncted/main/scripts/install.sh | sh -s -- --authorize-kwin "$(command -v pf)"
PF_SCREEN_BACKEND=kwin-shot2 pf info
```

KWin decides authorization through its normal application registry. Same-path
upgrades retain the registration; no sudo, logout or security-policy change is
needed on the tested Plasma 6.6 installation. See [desktop permissions](docs/desktop.md#native-kwin-screen-capture) for revocation and Go applications.

### Other installation methods

**Go developers:**

```sh
go install github.com/nskaggs/perfuncted/v2/cmd/pf@latest
```

**Flatpak:** Download `perfuncted.flatpak` from [GitHub Releases](https://github.com/nskaggs/perfuncted/releases), then run:

```sh
flatpak install --user ./perfuncted.flatpak
flatpak run io.github.nskaggs.perfuncted info
```

See [Flatpak setup and permissions](docs/flatpak.md) for more information.

## Quickstart

Open a disposable editor window and run:

```sh
pf info
pf window list
pf window activate "Editor" --verify
pf input type-literal "Hello from Perfuncted"
pf screen grab --out screenshot.png
```

Replace `Editor` with a matching window title. For duplicate titles, use
`pf window activate-id` with a stable ID from `pf window list`. Move, resize,
minimize, maximize, fullscreen, unfullscreen and get-geometry also accept
`--id` instead of a title. Input affects
the focused application; successful dispatch does not prove the application
accepted or saved the input.

## Capabilities

| Capability | Example commands |
| --- | --- |
| Windows and virtual desktops | `pf window list`, `pf window activate-id`, `pf desktop list` |
| Keyboard and pointer | `pf input type-literal`, `pf input click-window`, `pf input move` |
| Screen capture and visual search | `pf screen grab`, `pf find image`, `pf find text` |
| Accessibility | `pf a11y apps`, `pf a11y wait`, `pf a11y action` |
| Repeatable workflows | `pf run task.pf`, `pf workflow status` |
| Managed sessions | `pf session start`, `pf session check` |

Availability varies with compositor and permissions. Prefer stable window IDs,
explicit coordinate spaces, scoped semantic selectors and independently
observed postconditions.

## Desktop support

Perfuncted supports X11 and supported Wayland environments, including GNOME,
KDE Plasma and Sway. Run `pf info` to inspect available capabilities.
Wayland portals may require explicit consent; the GNOME native bridge may
require a logout and login.

**KDE Plasma Wayland (one-time setup):** native KWin screen capture includes
portal file pickers and other visible dialogs that the Screenshot portal can
omit. After installing the native CLI, review and run the
[authorization script](scripts/authorize-kwin.sh) once as the desktop owner:

```sh
curl -fsSL https://raw.githubusercontent.com/nskaggs/perfuncted/main/scripts/authorize-kwin.sh | sh
pf info
```

Perfuncted automatically prefers native KWin capture once authorized; no
per-command flag or Spectacle installation is needed. Without authorization
it may fall back to the portal, which can omit its own dialogs. For an
alternate installation path, removal, and Go-library integration, see
[native KWin capture](docs/desktop.md#native-kwin-screen-capture). See also
[desktop support](docs/desktop.md) and [Flatpak permissions](docs/flatpak.md).

## Go library

```sh
go get github.com/nskaggs/perfuncted/v2
```

```go
import "github.com/nskaggs/perfuncted/v2"

func enter() error {
    s, err := perfuncted.Open()
    if err != nil { return err }
    defer s.Close()
    return s.Do(perfuncted.Type("Hello"), perfuncted.Key("tab"))
}
```

See the [Go library guide](docs/go-api.md) and
[Go API reference](https://pkg.go.dev/github.com/nskaggs/perfuncted/v2).

## Documentation

- [CLI command reference](docs-cli/pf.md)
- [Desktop automation and permissions](docs/desktop.md)
- [Accessibility automation](docs/accessibility.md)
- [Flatpak installation](docs/flatpak.md)
- [Contributing](CONTRIBUTING.md)

## License

Apache 2.0. See [LICENSE](LICENSE).
