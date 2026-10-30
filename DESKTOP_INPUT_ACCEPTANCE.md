# Desktop input correctness and acceptance

This guide describes the red/green regressions added after the KDE/Firefox/MEGA incident. Test **on normal paths as well as failure paths** before invoking a real desktop: `go test -count=1 ./...`, `go test -race ./input ./cmd/pf`, `go test ./cmd/pf -run '^$' -fuzz '^FuzzWindowRelativePoint$' -fuzztime=5s`.

## Selection and permissions

`pf info` reports its selected backend. On KDE Wayland, Perfuncted tries compositor virtual input, then `/dev/uinput` configured from validated logical output geometry, and finally XTest when the compositor-wide options are unavailable. **XTest is limited to X11/XWayland windows**, and `pf info` warns about it; successful XTest initialization is not evidence that native Wayland windows can be controlled. Pure X11 still prefers XTest. Forced uinput (`PF_FORCE_INPUT=uinput`) never silently switches backends.

For full Wayland desktop input, opt in to a portal-authorized session using `pf --input-backend remote-desktop ...`. This requests normal user consent; `--remember-input-permission` explicitly requests persistent permission. There is no implicit portal consent or hidden credential extraction. The remote-desktop flag must be permitted on `pf info` as well as commands that require input.

## Coordinate conventions

- `pf input click` accepts backend-native global coordinates; use `pf input coordinate-space` to check whether the backend can declare that space.
- `pf input click-window --id <stable-id> --x N --y N` accepts **compositor-logical coordinates within the reported window geometry**. It rejects missing/ambiguous window identity, unknown geometry or unknown backend coordinate space. Window borders/client-area offsets are compositor-dependent; it is not a semantic element click.
- `pf input click-screen --x N --y N` translates physical screenshot pixels to output-logical coordinates and then to the declared backend coordinates. It requires authoritative single-output metadata; multi-output physical screenshot layout conversions are intentionally rejected until the exact per-output capture rectangles are available.
- Fractional scaling, negative monitor origins, out-of-bounds coordinates and near-integer-overflow positions are covered by deterministic tests and fuzz seeds. Do not infer a scaling ratio from browser CSS pixels.

## Stable identity and verification

`pf window list` prints a stable ID (UUID on Wayland, hexadecimal X11 identifier). `pf window activate-id <id> --verify` confirms focus using fresh window state; `pf window close-id <id> --verify` confirms disappearance. Unverified commands say an operation was *requested*, not accomplished. The title-based `pf window activate <title> --verify` provides focus verification for existing automation. Duplicate titles must use a stable ID.

`pf input select-file --id <file-chooser-id> --path /absolute/file.ext` checks that the file is present, activates and verifies the chooser, temporarily uses the clipboard for the absolute filename, and waits for the chooser to disappear before restoring the prior clipboard text. The helper does **not** create an upload dialog or claim a cloud upload occurred; confirm success in the target app. File chooser keyboard protocols differ; this helper uses the Ctrl+L / Ctrl+V / Enter sequence common to KDE/GTK choosers. It can be adjusted by targeted adapters later.

## Acceptance sequence (real desktop, after unit tests)

1. Run `pf info`, `pf output list` and `pf window list` without modifying desktop state. Check selected input backend, fractional scale, and stable IDs.
2. Open a **disposable test window** under native Wayland and under X11. Validate window IDs, activation, and explicit verification. Avoid using production browser windows as targets.
3. With permission, issue one click at the center of a disposable button and check its title/counter changed. Check it remains unchanged when input should not reach the target.
4. Open a disposable local file chooser, select an arbitrary temporary test file, and verify chooser closure and clipboard restoration.
5. Do not promote or claim backend-wide reliability solely from one desktop: capture compositor, backend, monitor topology, and acceptance outcomes. Automated synthetic tests cover GNOME bridge, Wayland virtual-input, portal EIS, XTest, uinput and no-backend fallback via existing backend tests plus new selection and validation matrices.

The tests make no claim that every compositor implements portal/EIS, uinput absolute positioning, or AT-SPI semantics identically. Those capabilities must remain explicit and independently verified.

## Completed acceptance on X1carbon (2026-10-30)

Host: KDE Plasma Wayland, one eDP-1 output, 1600×900 logical / 1920×1080 physical, scale 6/5. The revised `pf` selected `*input.UinputBackend` with validated logical bounds and advertised `global-logical` 1x1 mapping **only for this authoritative single-output configuration**. A disposable native-Wayland GTK test window identified by `{f75177b1-c70d-45ef-8022-46b2011e0f41}` was activated with verified focus. Clicks succeeded by direct uinput (800,430), by `click-window --x 280 --y 150` (mapped to 800,417), and by `click-screen --x 960 --y 520` (mapped to 800,433). GTK independently recorded three click events and changed its caption to `PF-AUDIT-WAYLAND-CLICK-3`. `close-id --verify` confirmed the test window was gone. This does not certify multi-output systems or alternative compositor implementations.

## Unclosed acceptance and coverage gaps (prioritized)

1. **P0 Remote Desktop/EIS protocol:** Test a complete authorized session with a synthetic portal + EIS peer, including session-creation denial, grant scope, missing pointer regions, malformed frames, mid-write disconnection, concurrent operations, revocation, canceled requests, restoration tokens and held-input release. The current operation-level coverage is low; missing initialization and canceled coordinate reports are now tested and fixed, but no in-session live EIS permission grant was exercised.
2. **P0 Output changes and hotplug:** Test multi-monitor mosaics (independent scale, negative origins, rotation, gaps, disconnected/reconnected displays, output reconfiguration while input is active). Unknown or multi-output coordinate mappings must fail closed. Coordinate conversion must never manufacture input into an unintended screen region.
3. **P1 XTest and uinput event confirmation:** Run isolated Xvfb/XWayland tests for actual button press/release, scroll directions, pointer location, cancellation, keyboard modifiers and physical keyboard layouts; run uinput against synthetic evdev consumers and real Wayland to confirm axis limits, release after cancellation, device disappearance, and simultaneous input sessions. Existing backend unit tests cover many of these but do not prove every compositor's event path.
4. **P1 Native GNOME bridge:** Add live GNOME Shell extension tests for pointer movement, focus, Unicode/paste, modifiers held by caller, long scrolls, cancellation and GNOME Shell restarts. New fake bridge tests cover normal clicks/scrolling and cancellation but not a live GNOME environment.
5. **P1 Browser and file chooser workflows:** Validate file selection against Firefox/GTK/KDE dialogs under X11 and Wayland and verify target app receives the selected file. Test chooser replacement, stale IDs, multiple dialogs with identical titles, clipboard changes during selection, Unicode paths, symlinks, unreadable files and cancelled user prompts. The current helper guarantees no fabricated upload result; end-to-end cloud upload is not guaranteed by file picker completion.
6. **P1 Screen capture and fractional scaling:** Add portal screenshot errors, permission denials/revocation, screen crop edges, mixed-output capture rectangles, rotation, HDR/pixel formats, long captures and stale output metadata; the screen package remains approximately half covered.
7. **P2 Semantic accessibility:** Add Firefox/AT-SPI enabled/disabled scenarios, changing DOM trees, inaccessible controls, focus and action confirmation; provide a browser-specific file input adapter only with explicit user-initiated target-file selection and without bypassing user permissions.

Do not treat aggregate coverage as a substitute for behavior coverage. The focused regression suite includes randomized seeded geometry tests, fuzz seeds, backend-selection matrices, nil/canceled constructor tests, KWin active-window parsing tests, EIS region/device overlap tests, and mocked CLI workflows, alongside existing normal-path tests.
