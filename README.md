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

### Other installation methods

**Go developers:**

```sh
go install github.com/nskaggs/perfuncted/cmd/pf@latest
```

**Flatpak:** Download `perfuncted.flatpak` from [GitHub Releases](https://github.com/nskaggs/perfuncted/releases), then run:

```sh
flatpak install --user ./perfuncted.flatpak
flatpak run io.github.nskaggs.perfuncted info
```

See [Flatpak setup and permissions](FLATPAK.md) for more information.

## See it in action

### Control windows and applications

```bash
# Find open windows
pf window list

# Activate Firefox and verify focus
pf window activate Firefox --verify

# Open a web page in Firefox
pf input type "{ctrl+l}"
pf input type "https://example.com{enter}"
```

### Capture and inspect your desktop

```bash
# Capture a screenshot
pf screen grab --out - > screenshot.png

# List applications with accessibility support
pf a11y apps

# Check available desktop capabilities
pf info
```

### Build scripts

Combine commands into repeatable workflows. Use `pf run` to execute a script
file or read commands from standard input.

```bash
pf run task.pf
```

## Desktop support

Perfuncted supports X11 and several Wayland environments, including GNOME,
KDE Plasma, and Sway. Available operations depend on the desktop and its
permissions. Run `pf info` to see which capabilities are available.

**GNOME Shell bridge:** Perfuncted includes a versioned GNOME Shell integration
for native window control, input, screenshots, and clipboard access. The native
CLI installs the bridge when a GNOME capability requires it. GNOME Shell may
require one logout and login to load it. The bundled bridge declares support
for GNOME Shell 46 through 51. Flatpak can use an existing host installation
of the bridge, but cannot install the host extension from inside its sandbox.

Wayland desktop portals may request user authorization for capture or input.
See [Flatpak setup and permissions](FLATPAK.md) for supported paths and
troubleshooting.

## Documentation

See the [CLI command reference](docs-cli/pf.md) for commands and options.
See [Contributing](CONTRIBUTING.md) for development and testing.

## Go library

Perfuncted is also available as a Go library for direct desktop automation.

```bash
go get github.com/nskaggs/perfuncted
```

See the [Go API reference](https://pkg.go.dev/github.com/nskaggs/perfuncted).

## License

Apache 2.0. See [LICENSE](LICENSE).
