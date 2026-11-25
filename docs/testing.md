# Desktop acceptance testing

Validate normal and failure paths before invoking a real desktop. See [desktop support](desktop-support.md) and [input automation](input.md) for the public contracts.

```sh
go test -count=1 ./...
go test -race ./input ./cmd/pf
go test ./cmd/pf -run '^$' -fuzz '^FuzzWindowRelativePoint$' -fuzztime=5s
```

## Acceptance sequence (real desktop, after unit tests)

1. Run `pf info`, `pf output list` and `pf window list` without modifying desktop state. Check selected input backend, fractional scale, and stable IDs.
2. Open a **disposable test window** under native Wayland and under X11. Validate window IDs, activation, and explicit verification. Avoid using production browser windows as targets.
3. With permission, issue one click at the center of a disposable button and check its title/counter changed. Check it remains unchanged when input should not reach the target.
4. Open a disposable local file chooser, select an arbitrary temporary test file, and verify chooser closure and clipboard restoration.
5. Do not promote or claim backend-wide reliability solely from one desktop: capture compositor, backend, monitor topology, and acceptance outcomes. Automated synthetic tests cover GNOME bridge, Wayland virtual-input, portal EIS, XTest, uinput and no-backend fallback via existing backend tests plus new selection and validation matrices.

The tests make no claim that every compositor implements portal/EIS, uinput absolute positioning, or AT-SPI semantics identically. Those capabilities must remain explicit and independently verified.

## Synthetic protocol acceptance

A deterministic, private `dbus-daemon` fixture now executes a complete synthetic Remote Desktop portal authorization: `CreateSession`, `SelectDevices`, `Start`, Unix-FD `ConnectToEIS`, the EIS handshake, seat capability binding, device discovery/resumption, keyboard and pointer delivery, and session close. Additional socket-backed cases cover scrolling, text, click, held-key/button release, device pause/revocation, malformed framing, grant denial, incomplete device scope, canceled requests, and invalid persistent restore tokens. The tests do not bypass or assert real desktop consent; a live user-authorized EIS session still requires separate acceptance.

A separate synthetic screenshot portal verifies successful PNG capture and crop/hash, explicit authorization denial, invalid URI, unreadable path, failed consent completion, cancellation, and serialized capture operations. CLI tests also reject negative-origin, mixed-scale, and disjoint multi-monitor screenshot-coordinate conversion and verify fail-closed behavior when a second monitor is connected. This exercises snapshot-level fail-closed routing; it does not prove in-flight hotplug atomicity, rotation support, or a compositor-independent multi-output coordinate transform.

These fixtures are unit-level protocol acceptance, not evidence of a successful Firefox upload, KDE/GTK file picker, live GNOME extension, or real compositor event delivery.



## Further validation

Exercise authorization denial and revocation, mixed-scale output changes, native compositor input and capture, file chooser completion, and scoped accessibility operations on disposable applications. Synthetic tests and statement coverage do not substitute for observed real desktop events.
