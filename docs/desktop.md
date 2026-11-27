# Desktop automation

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
See [Flatpak setup and permissions](flatpak.md) for supported paths and
troubleshooting.

## Selection and permissions

`pf info` reports its selected backend. On KDE Wayland, Perfuncted tries compositor virtual input, then `/dev/uinput` configured from validated logical output geometry, and finally XTest when the compositor-wide options are unavailable. **XTest is limited to X11/XWayland windows**, and `pf info` warns about it; successful XTest initialization is not evidence that native Wayland windows can be controlled. Pure X11 still prefers XTest. Forced uinput (`PF_FORCE_INPUT=uinput`) never silently switches backends.

For full Wayland desktop input, opt in to a portal-authorized session using `pf --input-backend remote-desktop ...`. This requests normal user consent; `--remember-input-permission` explicitly requests persistent permission. There is no implicit portal consent or hidden credential extraction. The remote-desktop flag must be permitted on `pf info` as well as commands that require input.

## Smooth pointer motion

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
and `input.MotionOptions`. Smooth movement without an explicit origin reads the
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

## Global hotkey and mouse observation

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
attest physical-vs-injected provenance. The Go equivalent is `Session.InputObservation().Listen(ctx,
input.ObservationFilter{Combo: "F8", Transition: "press"})`, returning a
subscription with `Events`, `Err`, and `Close`.

The current native-X11 implementation uses non-grabbing XInput2 raw key and
button events. Controlled Xvfb delivery is tested; real physical-input and
cross-focus acceptance on a native Xorg desktop remains unverified. KDE, GNOME, Sway/wlroots and all other Wayland compositors, XWayland,
and unknown sessions return explicit unsupported. Neither persistent
authorization nor privileged device access is requested.

## On-demand key and mouse state

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
The Go equivalent is `Session.InputObservation().KeyState`, `LockState` or
`MouseButtonState`; check `errors.Is(err, perfuncted.ErrUnsupported)` when the
selected session cannot observe global state. Successful queries include an
observation timestamp and provider so callers can distinguish a current X11
read from state inferred from earlier automation.

Subscriptions have a fixed 64-event queue; overflow ends the subscription
with a terminal `ErrObservationLoss` rather than silently dropping key-up
history. Cancellation, source disconnection and Session.Close retire
listeners. Keyboard mapping/hierarchy uncertainty on X11 fails closed.
Input observation does not guarantee physical-input provenance.

## Coordinate conventions

- `pf input click` accepts backend-native global coordinates; use `pf input coordinate-space` to check whether the backend can declare that space.
- `pf input click-window --id <stable-id> --x N --y N` accepts **compositor-logical coordinates within the reported window geometry**. It rejects missing/ambiguous window identity, unknown geometry or unknown backend coordinate space. Window borders/client-area offsets are compositor-dependent; it is not a semantic element click.
- `pf input click-screen --x N --y N` translates physical screenshot pixels to output-logical coordinates and then to the declared backend coordinates. It requires authoritative single-output metadata; multi-output physical screenshot layout conversions are intentionally rejected until the exact per-output capture rectangles are available.
- Fractional scaling, negative monitor origins, out-of-bounds coordinates and near-integer-overflow positions are covered by deterministic tests and fuzz seeds. Do not infer a scaling ratio from browser CSS pixels.

## Stable identity and verification

`pf window list` prints a stable ID (UUID on Wayland, hexadecimal X11 identifier). `pf window activate-id <id> --verify` confirms focus using fresh window state; `pf window close-id <id> --verify` confirms disappearance. Unverified commands say an operation was *requested*, not accomplished. The title-based `pf window activate <title> --verify` provides focus verification for existing automation. Duplicate titles must use a stable ID.

`pf input select-file --id <file-chooser-id> --path /absolute/file.ext` checks that the file is present, activates and verifies the chooser, temporarily uses the clipboard for the absolute filename, and waits for the chooser to disappear before restoring the prior clipboard text. The helper does **not** create an upload dialog or claim a cloud upload occurred; confirm success in the target app. File chooser keyboard protocols differ; this helper uses the Ctrl+L / Ctrl+V / Enter sequence common to KDE/GTK choosers. It can be adjusted by targeted adapters later.

## Capture and inspect your desktop

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

See the [visual search commands](../docs-cli/pf_find.md) for pixel, template and OCR search.

## Workflows and sessions

Run a script with `pf run task.pf` and inspect active workflows with `pf workflow status`. For managed desktops, see [session commands](../docs-cli/pf_session.md). For scoped semantic controls, see [accessibility automation](accessibility.md).

## Window selection and command results

Use `pf window list` to discover stable IDs. Titles used by window management
commands are case-insensitive substrings and must identify exactly one window.
For direct CLI commands, prefer explicit IDs when titles change or are duplicated:

```sh
pf window move --id 0x2a --x 100 --y 200
pf window resize --id 0x2a --w 1280 --h 720
pf window maximize --id 0x2a
pf window get-geometry --id 0x2a --output json
```

The same `--id` alternative is available for minimize, fullscreen and
unfullscreen. Existing title arguments and `--title` flags remain valid.
Do not combine title and ID selectors. Use activate-id and close-id for the
existing stable-ID activation and close commands. IDs are scoped to the live
desktop/backend; rediscover them after a window closes or the session changes.
A stable ID is not an atomic lock against focus or identity changes.
The `pf run` workflow grammar remains unchanged.

`window is-visible` tests whether one matching window is discoverable, not
whether it is unminimized, unobstructed or actually displaying visible pixels.
Window geometry is compositor-reported; plain geometry output uses corner
coordinates, JSON uses origin and dimensions.

Mutation success normally acknowledges dispatch, not the eventual compositor
state. Activation and close-id have optional verification; verification
failure does not prove that dispatch failed. Do not automatically repeat a
mutation when its completion is uncertain, including after cancellation,
a failed synchronization, or an output-write failure. Observe the intended
postcondition independently before deciding whether to retry.

## Typing and capture semantics

Use `pf input type-literal` for literal text, including braces and key-like
strings. `pf input type` interprets brace-delimited key sequences; this
also applies to text read through `--stdin`. The Go convenience Type is
literal, whereas InputBundle.Type interprets key sequences. Input goes to
the relevant focused application; successful dispatch is not acceptance or
proof of a saved result.

Screen grab by window ID, exact case-insensitive title or PID captures the
visible screen rectangle of a unique window unless `--background` is
explicitly requested. A visible screen crop may include other windows.
Background capture uses the target's own X11 pixmap when supported; it is
not a foreground fallback or guarantee of freshness. Region hashes are
unchanged and refer to the specified screen rectangle. Use reported logical
window geometry, physical screenshot pixels and backend-native input
coordinates only with the corresponding operations; do not assume that
these spaces coincide on scaled or multi-output desktops.

## Native KWin screen capture

On KDE Plasma Wayland, Perfuncted prefers KWin's restricted
`org.kde.KWin.ScreenShot2` interface. KWin matches the calling executable to
the installed desktop application's absolute Exec path and declared restricted
interface. A CLI installed without an application entry may be denied even
when the Screenshot portal is available. The portal can omit its own dialogs.

If the desktop owner authorizes Perfuncted for native screen capture, run
`sh scripts/install.sh --authorize-kwin` after installing the executable to
`~/.local/bin/pf` (or pass its absolute installed path). This adds only the
Perfuncted application entry for the current user and refreshes KDE's
application registry. No permission checks are disabled and no privileged
helper is used. The permission applies to that executable, not all programs;
Go applications embedding the library need their own application registration.
Remove the registration with `sh scripts/install.sh --remove-kwin`.

Use `PF_SCREEN_BACKEND=kwin-shot2 pf info` and inspect the screen capability.
This explicitly requires native KWin capture and does not silently select a
portal if KWin denies access. Then run
`PF_SCREEN_BACKEND=kwin-shot2 pf screen grab --out /absolute/path.png`.
A KWin workspace capture requests native resolution; rectangular capture crops
that image in pixel coordinates, rather than interpreting physical pixels as
KWin logical coordinates. The single-output fractional-DPI path is supported;
mixed-scale multi-output pointer alignment needs separate verification.
