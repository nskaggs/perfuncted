# Input automation

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
attest physical-vs-injected provenance. The Go equivalent is `Session.InputObservation.Listen(ctx,
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

## Coordinate conventions

- `pf input click` accepts backend-native global coordinates; use `pf input coordinate-space` to check whether the backend can declare that space.
- `pf input click-window --id <stable-id> --x N --y N` accepts **compositor-logical coordinates within the reported window geometry**. It rejects missing/ambiguous window identity, unknown geometry or unknown backend coordinate space. Window borders/client-area offsets are compositor-dependent; it is not a semantic element click.
- `pf input click-screen --x N --y N` translates physical screenshot pixels to output-logical coordinates and then to the declared backend coordinates. It requires authoritative single-output metadata; multi-output physical screenshot layout conversions are intentionally rejected until the exact per-output capture rectangles are available.
- Fractional scaling, negative monitor origins, out-of-bounds coordinates and near-integer-overflow positions are covered by deterministic tests and fuzz seeds. Do not infer a scaling ratio from browser CSS pixels.

## Stable identity and verification

`pf window list` prints a stable ID (UUID on Wayland, hexadecimal X11 identifier). `pf window activate-id <id> --verify` confirms focus using fresh window state; `pf window close-id <id> --verify` confirms disappearance. Unverified commands say an operation was *requested*, not accomplished. The title-based `pf window activate <title> --verify` provides focus verification for existing automation. Duplicate titles must use a stable ID.

`pf input select-file --id <file-chooser-id> --path /absolute/file.ext` checks that the file is present, activates and verifies the chooser, temporarily uses the clipboard for the absolute filename, and waits for the chooser to disappear before restoring the prior clipboard text. The helper does **not** create an upload dialog or claim a cloud upload occurred; confirm success in the target app. File chooser keyboard protocols differ; this helper uses the Ctrl+L / Ctrl+V / Enter sequence common to KDE/GTK choosers. It can be adjusted by targeted adapters later.
