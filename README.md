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

### Global hotkey and mouse observation (issue #128)

Global observation is a **separate optional capability** from keyboard/mouse
injection. Only explicitly requested named shortcuts or mouse-button filters
are observed; no typed characters are logged.

```sh
pf input hotkey listen --combo F8 --event press --output json
pf input hotkey listen --combo Ctrl+Shift+P --event both --timeout 30s
pf input hotkey listen --button left --event press --output json
```

Press Ctrl+C to cancel and unregister. JSON reports `kind`, `id`,
`transition`, `time`, and `source`. The event source does **not**
attest physical-vs-injected provenance. The Go equivalent is `Session.InputObservation.Listen(ctx,
input.ObservationFilter{Combo: "F8", Transition: "press"})`, returning a
subscription with `Events`, `Err`, and `Close`.

The current native-X11 implementation uses non-grabbing XInput2 raw key and
button events. Controlled Xvfb delivery is tested; real physical-input and
cross-focus acceptance on a native Xorg desktop remains unverified, so issue
#128 is not considered complete. KDE, GNOME, Sway/wlroots and all other Wayland compositors, XWayland,
and unknown sessions return explicit unsupported. Neither persistent
authorization nor privileged device access is requested.

### On-demand key and mouse state (issue #139)

The optional input-observation capability also supports single, read-only
state queries. On native X11 with XI2, key and button results are aggregate
logical server state; XTest or other synthetic input can contribute, so
`provenance` is `unknown`. The command does not infer state from earlier
Perfuncted actions, record a key history, or request root/evdev access.
Wayland and XWayland return unsupported because they do not grant this global
state query through the current backend.

```sh
pf input state --key shift
pf input state --lock capslock
pf input state --button left
```

Lock-key held state (for example, `--key capslock`) is distinct from the
enabled mode (`--lock capslock`). The X11 backend can query Caps Lock, Num Lock
and Scroll Lock when the current modifier map identifies an unambiguous lock
bit; unmapped or ambiguous lock states return unsupported.
The Go equivalent is `Session.InputObservation.KeyState`, `LockState` or
`MouseButtonState`; check `errors.Is(err, perfuncted.ErrUnsupported)` when the
selected session cannot observe global state. Successful queries include an
observation timestamp and provider so callers can distinguish a current X11
read from state inferred from earlier automation.

Subscriptions have a fixed 64-event queue; overflow ends the subscription
with a terminal `ErrObservationLoss` rather than silently dropping key-up
history. Cancellation, source disconnection and Session.Close retire
listeners. Keyboard mapping/hierarchy uncertainty on X11 fails closed.
Input observation does not guarantee physical-input provenance.

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

# Capture a selected window's visible screen rectangle by stable ID, exact title, or unique PID
pf screen grab --window-id 123 --out window.png --metadata window.json

# Capture that X11 window's own Composite pixmap without activating it
pf screen grab --window-id 123 --background --out window.png --metadata window.json

# List applications with accessibility support
pf a11y apps

# Check available desktop capabilities
pf info
```

Window-targeted capture uses window-manager-reported bounds and returns the
visible desktop composite for that rectangle. Overlapping windows may appear;
minimized or clipped targets and backends without usable geometry are rejected.
The optional JSON sidecar records the stable window ID, desktop rectangle,
image pixel bounds and scale, capture mode, geometry source, and occlusion
semantics. The `--window-title` selector matches the complete title
case-insensitively; `--pid` fails if the process owns more than one window.
With `--out -`, PNG bytes remain alone on stdout and the bounds report is
written to stderr.

`--background` requests a target-only buffer from an already redirected,
mapped X11 window. It never activates or redirects a window and never falls
back to the visible screen crop. The metadata state is `foreground`,
`background-visible`, `occluded`, or `unknown`; occlusion is conservatively
estimated from mapped top-level window bounding boxes, so shaped or
translucent windows can be reported as occluding. Minimized or unmapped
targets, X11 windows without an existing Composite pixmap, and non-X11 screen
backends return unsupported. The captured window buffer may be stale; its
freshness is not guaranteed. Current XTest/EIS input remains global and is not
treated as background-targeted input. The in-process API is
`ScreenBundle.CaptureWindowBackground`. Accessible controls can still be
invoked through the separately scoped `pf a11y action` operation when the
accessibility provider exposes the control; semantic actions are not raw
keyboard or mouse delivery.

### Wait for an accessible control

Use a scoped semantic selector and an explicit timeout to wait for a control
to appear, disappear, receive focus, become visible or enabled, or change its
accessible value:

```bash
pf a11y wait --app Editor --role button --name Save --until enabled --timeout 5s
pf a11y wait --window "Editor" --role status bar --name Status --until value-changed --timeout 10s --json
```

`--until` accepts `present`, `absent`, `focused`, `unfocused`, `visible`,
`hidden`, `enabled`, `disabled`, and `value-changed`. Existing `--state` flags
remain initial selector filters. Once the first unique control is resolved,
the wait follows that same node identity while its state or value changes.
Every condition uses fresh complete AT-SPI snapshots. Accessibility
notifications only wake the wait; a new snapshot must confirm success. A
timeout returns `context.DeadlineExceeded` in Go and a nonzero CLI result.
Ambiguous matches, incomplete snapshots, unsupported value properties, and a
lost application/window scope produce errors. `--visible-only` cannot prove
absence or a hidden state and is rejected for those conditions.

For `value-changed`, the first complete observation of the uniquely matched
control is the baseline. The wait compares `AT-SPI Value.CurrentValue` when
available, otherwise complete non-redacted accessible text. It does not infer
changes from screenshots. `visible` follows the provider's AT-SPI VISIBLE
state; it does not claim that a control is unobscured or physically on screen.
Waits read accessibility data and do not request input or screenshot access.
Sensitive values remain redacted unless `--allow-sensitive` is selected.

### Invoke an action and verify its outcome

`pf a11y action` remains a one-shot operation when no expectation is supplied.
To wait for accessible confirmation, use `--expect-text` or
`--expect-state changed` with a positive timeout:

```bash
pf a11y action --app Editor --role button --name Save --expect-text Saved --timeout 5s --json
pf a11y action --app Editor --role button --name Save --expect-state changed --timeout 5s --json
```

Verified JSON includes the dispatch receipt, outcome proof, and wait evidence.
On a verification timeout it reports `verification-timeout`, keeps the
accepted dispatch receipt, and exits nonzero. A timeout never retries the
action. `changed` captures a complete baseline before dispatch and compares
the same accessible object identity afterward. Verification means the
condition was observed; it does not prove that the action caused the change.

The Go action-and-wait API accepts the existing window and accessibility
conditions. For a caller-defined screen condition, capture its baseline before
the action and declare the required screen capability so unavailable screen
observation fails before dispatch:

```go
baseline, err := session.Screen.GrabRegionHash(ctx, region)
if err != nil {
    return err
}
screenChanged := perfuncted.WithConditionCapabilities(
    perfuncted.Predicate("screen region changed", func(ctx context.Context) (bool, error) {
        current, err := session.Screen.GrabRegionHash(ctx, region)
        return current != baseline, err
    }),
    perfuncted.CapabilityScreen,
)
receipt, evidence, err := session.InvokeAccessibilityActionAndWait(
    ctx, root, query, "", accessibility.SnapshotOptions{}, screenChanged,
)
```

`WindowExists`, `WindowClosed`, and accessibility condition builders declare
their own observation capabilities. Caller-defined conditions should use
`WithConditionCapabilities` when they read session bundles.

The Go API uses the same bounded wait engine:

```go
locator := session.Accessibility.LocatorForApplication(
    accessibility.ApplicationFilter{Name: "Editor"},
    accessibility.Selector{Role: "button", Name: "Save"},
    accessibility.SnapshotOptions{},
)
node, evidence, err := locator.WaitForState(ctx, perfuncted.AccessibilityWaitEnabled, 5*time.Second)
```

If two Save buttons match, the wait returns an ambiguity error rather than
choosing one. If no unique target is available before the deadline, it returns
a timeout; if the selected application or window changes during the wait, it
returns `perfuncted.ErrManagedScopeChanged`. If the matched control disappears
or is replaced before its state is observed, Go returns
`perfuncted.ErrAccessibilityTargetChanged`.

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
