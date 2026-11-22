# Screenshot capture benchmarks

These benchmarks measure `Screenshotter.Grab` results. They are separate from
`BenchmarkDesktopRegionHash`, `BenchmarkDecodeBGRA`, and
`BenchmarkCapturePNGEncode`: hashes and pixel conversion do not measure a full
screenshot operation, and PNG encoding is optional caller work.

## Controlled real capture runs

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

## Lifecycle and authorization notes

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
commands. No attended desktop or consent experiment was run for this report.
Do not infer whether permission was cached or prompted from a headless run, and
do not redirect a denied portal request to a more privileged backend.

## Recorded controlled results

Measured on 2026-11-22 at `e33963091c6a71a3e864c738c92fb124224e1d78` before
the benchmark changes, with Go 1.27.4 on Linux/amd64, Intel Core i7-10510U,
8 CPUs. The working tree contained the uncommitted issue #141 implementation.
X11 used a private 1024×768 Xvfb server; Wayland used the existing private
1024×768 Sway session, which selected `*screen.WlrScreencopyBackend`.

Each Go benchmark average below used three timed operations. Warm distribution
metrics used 50 additional direct capture calls; allocations are per timed Go
benchmark operation. X11 backend-open and first-frame figures are one measured
operation each and have no percentile claim. Values describe this host and
these private compositor fixtures only.

| Source/mode | Mean ns/op | p50 | p95 | min–max | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Xvfb `BackendOpen` | 243,334 | — | — | one sample | 38,176 | 24 |
| Xvfb `FirstFull` | 3,346,861 | — | — | one sample | 3,149,101 | 13 |
| Xvfb `WarmFull` | 2,951,326 | 2.91 ms | 4.29 ms | 2.54–4.87 ms | 9,447,378 | 39 |
| Xvfb `WarmRegion256` | 304,972 | 0.24 ms | 0.45 ms | 0.21–0.67 ms | 796,664 | 40 |
| Sway `WarmFull` | 16,203,160 | 16.13 ms | 20.03 ms | 11.09–20.35 ms | 3,152,152 | 92 |
| Sway `WarmRegion256` | 16,724,261 | 16.14 ms | 17.00 ms | 7.94–18.34 ms | 268,557 | 92 |
| Sway `SessionFacadeWarmFull` | 16,409,166 | 16.43 ms | 20.88 ms | 9.95–21.11 ms | 3,152,824 | 100 |
| Sway `FirstFull` | 17,887,491 | — | — | 3 operations | 3,182,466 | 496 |
| Sway `OpenAndFirstFull` | 327,072,782 | — | — | 3 operations | 3,517,821 | 2,955 |

`BackendOpen` is a single X11 backend constructor sample; `FirstFull` excludes
backend construction and measures only the first `Grab`. Sway
`OpenAndFirstFull` includes managed headless session startup and the first
backend `Grab`, while cleanup is outside the timer. These measurements do not
include a real attended desktop or portal consent flow. No attended GNOME, KDE,
or portal measurements were run, so permission-prompt behavior remains
unknown. Do not treat these numbers as polling recommendations or as a
cross-machine regression baseline.

Never add screenshot files or raw pixel payloads to benchmark artifacts.
