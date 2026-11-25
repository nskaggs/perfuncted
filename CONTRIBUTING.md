# Contributing

## Prerequisites

- Go 1.27.0
- [`just`](https://github.com/casey/just)
- A Linux desktop session for local runtime checks

Install the repository's development tools with:

```bash
just install-dev-tools
```

The integration suite additionally needs the display, Wayland, X11, D-Bus,
clipboard, and test-application packages listed in
[the CI workflow](.github/workflows/ci.yml).

## Checks

Use the fast pre-commit gate while iterating:

```bash
just precommit
```

Before submitting a change, run the full static and unit suite:

```bash
just quality
```

Run integration checks sequentially because they share managed display and
temporary-session resources:

```bash
just test-integration
```

For CLI changes, regenerate and verify the checked-in reference:

```bash
just docs
just check-api-sync
```

Keep generated files formatted, preserve the public API contract, and include
tests for behavior changes.

## Documentation and examples

Keep committed content suitable for public distribution. Use synthetic examples,
generic paths and reproducible fixtures. Keep local investigation notes,
implementation plans, diagnostic logs, credentials and machine-specific
identifiers outside the repository. Explain behavior and observable outcomes,
not the circumstances in which a defect was discovered.

Regenerate CLI documentation from its command sources rather than editing
generated reference pages directly.

## Desktop acceptance testing

Validate normal and failure paths before invoking a real desktop. See [desktop automation](docs/desktop.md) for the public contracts.

```sh
go test -count=1 ./...
go test -race ./input ./cmd/pf
go test ./cmd/pf -run '^$' -fuzz '^FuzzWindowRelativePoint$' -fuzztime=5s
```

### Acceptance sequence (real desktop, after unit tests)

1. Run `pf info`, `pf output list` and `pf window list` without modifying desktop state. Check selected input backend, fractional scale, and stable IDs.
2. Open a **disposable test window** under native Wayland and under X11. Validate window IDs, activation, and explicit verification. Avoid using production browser windows as targets.
3. With permission, issue one click at the center of a disposable button and check its title/counter changed. Check it remains unchanged when input should not reach the target.
4. Open a disposable local file chooser, select an arbitrary temporary test file, and verify chooser closure and clipboard restoration.
5. Do not promote or claim backend-wide reliability solely from one desktop: capture compositor, backend, monitor topology, and acceptance outcomes. Automated synthetic tests cover GNOME bridge, Wayland virtual-input, portal EIS, XTest, uinput and no-backend fallback via existing backend tests plus new selection and validation matrices.

The tests make no claim that every compositor implements portal/EIS, uinput absolute positioning, or AT-SPI semantics identically. Those capabilities must remain explicit and independently verified.

### Synthetic protocol acceptance

A deterministic, private `dbus-daemon` fixture executes a complete synthetic Remote Desktop portal authorization: `CreateSession`, `SelectDevices`, `Start`, Unix-FD `ConnectToEIS`, the EIS handshake, seat capability binding, device discovery/resumption, keyboard and pointer delivery, and session close. Additional socket-backed cases cover scrolling, text, click, held-key/button release, device pause/revocation, malformed framing, grant denial, incomplete device scope, canceled requests, and invalid persistent restore tokens. The tests do not bypass or assert real desktop consent; a live user-authorized EIS session still requires separate acceptance.

A separate synthetic screenshot portal verifies successful PNG capture and crop/hash, explicit authorization denial, invalid URI, unreadable path, failed consent completion, cancellation, and serialized capture operations. CLI tests also reject negative-origin, mixed-scale, and disjoint multi-monitor screenshot-coordinate conversion and verify fail-closed behavior when a second monitor is connected. This exercises snapshot-level fail-closed routing; it does not prove in-flight hotplug atomicity, rotation support, or a compositor-independent multi-output coordinate transform.

These fixtures are unit-level protocol acceptance, not evidence of a successful Firefox upload, KDE/GTK file picker, live GNOME extension, or real compositor event delivery.

### Further validation

Exercise authorization denial and revocation, mixed-scale output changes, native compositor input and capture, file chooser completion, and scoped accessibility operations on disposable applications. Synthetic tests and statement coverage do not substitute for observed real desktop events.

## Screenshot capture benchmarks

These benchmarks measure `Screenshotter.Grab` results. They are separate from
`BenchmarkDesktopRegionHash`, `BenchmarkDecodeBGRA`, and
`BenchmarkCapturePNGEncode`: hashes and pixel conversion do not measure a full
screenshot operation, and PNG encoding is optional caller work.

### Controlled real capture runs

The repository's `desktopbench` build tag and
`PERFUNCTED_CAPTURE_BENCH_REAL=1` opt-in are both required for real capture
benchmarks. The X11 benchmark starts a private Xvfb server and passes its
display directly to `NewX11Backend`; it never reads the caller's `DISPLAY`.
The root package desktop benchmark starts the existing private headless Sway
session. Neither path stores screenshot pixels.

```sh
export GOWORK=off
export PERFUNCTED_CAPTURE_BENCH_REAL=1
CGO_ENABLED=0 go test -tags=desktopbench ./screen -run '^$' \
  -bench '^BenchmarkCaptureX11$' -benchmem -benchtime=3x -count=3 -v
CGO_ENABLED=0 go test -tags=desktopbench . -run '^$' \
  -bench '^BenchmarkDesktopCapture$' -benchmem -benchtime=3x -count=3 -v
```

For the ordinary headless Wayland performance lane, use `just desktop-benchmark`.
The reported Go `ns/op`, `B/op`, and `allocs/op` cover the timed operation.
Warm full and region runs also gather 50 direct `Grab` samples outside the Go
benchmark timer and report nearest-rank p50 and p95. First-frame runs use
repeated fresh managed sessions; their Go average is reported, and p95 is
omitted because the benchmark's small iteration count is not enough to support
that percentile. Warm sample output includes min, median, p95, and max. The
Sway lane labels its underlying backend type and measures
both direct backend `Grab` and the public `ScreenBundle.Grab` facade as separate
modes. A 256×256 region is used only when the output geometry supports it; the
actual request and returned bounds are logged.

Each run labels its source, concrete backend type, protocol or managed
compositor, output geometry, Go version, OS/architecture, and CPU count.
Display socket values, screenshot data, window titles, portal URIs, and tokens
are excluded. Missing Xvfb or an unsupported output is reported as skipped or
failed setup, never as a zero-latency result.

### Lifecycle and authorization notes

The X11 case opens a direct X11 connection and issues `Grab` for each frame.
The managed Sway case logs the selected Go backend type; inspect that type
before comparing it with a different machine or compositor. Existing portal
implementation opens one D-Bus connection per backend, then issues a new
Screenshot request for every `Grab`, serializes requests, decodes the full
workspace PNG, and crops a requested region in process. A new request does not
establish whether consent was newly prompted or remembered. Ext-image-copy
backends establish capture proxies during backend construction and then capture
frames through that state. These are implementation lifecycle notes, not
latency claims.

Attended GNOME, KDE, and portal runs are not part of the default benchmark
commands.
Do not infer whether permission was cached or prompted from a headless run, and
do not redirect a denied portal request to a more privileged backend.

Publish benchmark results with the commit, runtime, compositor, geometry, and measurement method; omit machine identifiers, raw pixels and unrelated local diagnostics.

## Release workflow

Releases are built by the GitHub Actions release workflow from a version tag.
The workflow reruns the deterministic quality gates before building the Go
artifacts and attaching the Flatpak bundle alongside the other release
artifacts.

Prerelease tags (anything matching `v*-rc*`, `v*-alpha*`, `v*-beta*`,
`v*-pre*`, or `v*-dev*`) do **not** trigger a release: they are excluded by the
workflow trigger and skipped by the in-job guard. They publish the module
version on the proxy when pushed, without creating a GitHub release. Only clean
`vX.Y.Z` tags create a release.

Before creating a release tag, run:

```bash
just quality
just test-integration
just test-release-static
```

CLI documentation is generated from the command sources and should be current
in the tagged tree:

```bash
just check-generate
just check-docs
just check-api-sync
```

The release workflow is defined in
[`.github/workflows/release.yml`](.github/workflows/release.yml).

### Release notes

For a curated release body, add `.github/release-notes/vX.Y.Z.md` before creating the
tag. The release workflow uses that file when it exists. If no curated file is
present, it generates notes from the commit history via the GitHub
generate-notes API. Keep the history thematic so the generated fallback notes
read well:

- Open significant changes with a one- or two-sentence summary.
- Group related work in focused commits under short headings.
- Use dry, factual bullets for user-visible changes and validation.
- Omit commit counts and extended narrative; the comparison link provides the
  complete commit list.
