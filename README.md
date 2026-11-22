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


### Smooth pointer motion

Immediate pointer moves and drags remain the default. Use `--duration` to
enable timed interpolation, with `--easing linear|ease-in-out`, optional
`--curve` (-0.5 to 0.5), `--variation` (0 to 16 backend units), and
`--seed` for repeatable paths:

```sh
pf input move --x 800 --y 450
pf input move --from-x 100 --from-y 100 --x 800 --y 450 --duration 500ms --easing ease-in-out
pf input drag-and-drop --x1 100 --y1 100 --x2 800 --y2 450 --duration 700ms --curve 0.2
```

The Go equivalents are `Input.MouseMoveSmooth`, `Input.DragAndDropSmooth`
and `MotionOptions`. Smooth movement without an explicit origin reads the
cursor position; backends unable to report it (including portal/EIS and
wl-virtual) require both `--from-x` and `--from-y` or return unsupported.
Explicit origins are not verified as the cursor's real location. Coordinates
are in backend-native absolute units, with no implicit scale conversion.
Timed moves and drags require an authoritative `PointerCoordinateSpace` report
(logical or physical with a valid backend scale). The existing XTest backend
does not advertise this report; smooth input there is unsupported until
coordinate provenance is implemented. Portal/EIS and wl-virtual report logical
coordinates, but require an explicit origin for smooth moves when pointer
position is unavailable. Missing, invalid, or ambiguous coordinate reports
fail before input dispatch. Immediate input does not require this metadata.

The maximum duration is 10 seconds, with approximately 60 Hz sampling capped
at 120 intervals; overdue intermediate samples may be dropped. Endpoints are
exact. The **bounded curved envelope** expands the axis-aligned endpoint
rectangle by `0.5 × |deltaY| + variation` horizontally and
`0.5 × |deltaX| + variation` vertically (with integer rounding at the
boundary). A curve of 0.5 therefore creates a meaningful perpendicular bend
on a horizontal, vertical, or diagonal path without flattening it. The
specified `curve` controls signed perpendicular deflection, while variation
is seeded, deterministic, and limited to 0–16 backend units with zero endpoint
displacement. Intermediate samples are checked against this envelope, and
authoritative backend validators check **every** path point before input. Drags hold and release the primary
button, with best-effort cleanup after errors or cancellation. A
zero-distance smooth drag performs one start move and one press/release without
waiting or emitting redundant timed motion. Scheduling is
best effort and actual UI hover/drag recognition and compositor permissions
require real desktop checks. These motion profiles do not establish human
input or promise anti-cheat evasion.

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

```go
import "github.com/nskaggs/perfuncted"

func enter() error {
    s, err := perfuncted.OpenDefault()
    if err != nil { return err }
    defer s.Close()
    return s.Do(perfuncted.Type("Invoice {A} + tax"),
        perfuncted.Key("tab"), perfuncted.Type("12.34"))
}
```

By default, `OpenDefault` requires input before returning and may prompt for Wayland
authorization. Request additional capabilities with `Require` or `Optional`.
`Type` sends literal text; `Key` sends a named key, `Hotkey` sends a chord,
and `TypeKeys` explicitly interprets key syntax. The batch targets the
currently focused application and stops at the first error. A successful
dispatch does not prove the application accepted or saved the input.

On X11, literal Unicode text can fail when the character is absent from the active
X keymap; this is an input-backend limitation, not a shortcut parse.

For an exact-title, window-targeted sequence, request the window capability:

~~~go
s, err := perfuncted.OpenDefault(perfuncted.Require(perfuncted.CapabilityWindows))
if err != nil { return err }
defer s.Close()
return s.Window("Editor").Do(perfuncted.Type("Hello"), perfuncted.Key("tab"))
~~~

Window-targeted batches select exactly one window, activate it once, wait for
observed focus, and recheck the selected identity and active state before
each input step. Missing or ambiguous windows, missing identity metadata,
unsupported focus observation and focus loss fail closed without redirecting
subsequent input. Window focus is not an atomic lock, and a recycled native
ID with identical published metadata cannot be distinguished. For stronger
semantic scoping and independently verified outcomes, use
AccessibilityLocator.ClickAndWait or FillAndWait with a postcondition.

Use `s.WithContext(ctx).Do(...)` or `s.WithTimeout(duration).Do(...)` for a
bounded batch. `StepError` identifies the failed step and unwraps its cause.
For receipts, explicit desktop configuration, accessibility transactions, and
other advanced control, use the existing session bundles. In particular,
`AccessibilityLocator.ClickAndWait` returns action and observation evidence;
conditions such as `AccessibilityTextContains` can express a postcondition.

See the [Go API reference](https://pkg.go.dev/github.com/nskaggs/perfuncted).

## License

Apache 2.0. See [LICENSE](LICENSE).
