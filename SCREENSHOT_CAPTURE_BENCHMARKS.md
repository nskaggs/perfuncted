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

Measured on 2026-11-22 at source commit
`93707ff2cf01170c7787fc33079446b886fe0d10` on a clean tree, with Go 1.27.4 on
Linux/amd64, Intel Core i7-2600 at 3.40 GHz, and 8 logical CPUs. X11
used a private 1024×768 no-reset Xvfb server. Wayland used a private 1024×768
Sway session and selected `*screen.WlrScreencopyBackend`. The benchmark
commands ran with desktop/profile environment variables unset.

Each benchmark used `-benchtime=3x -count=3`: three operations per trial and
three independent trials. The mean column is the median of the three Go
`ns/op` results. For warm modes, each trial collected 50 additional direct
`Grab` samples outside the Go benchmark timer; p50 and p95 are the medians of
the three trial percentiles, and min–max is the envelope across all 150
samples. `B/op` and `allocs/op` are the medians across trials. The cold-mode
min–max column gives the range of the three trial means; percentile claims are
omitted because three operations per trial are insufficient.

| Source/mode | Mean ns/op | p50 | p95 | min–max | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Xvfb `BackendOpen` | 407,129 | — | — | 386–412 µs (trial means) | 102,973 | 78 |
| Xvfb `FirstFull` | 7,489,791 | — | — | 5.91–9.57 ms (trial means) | 9,447,304 | 39 |
| Xvfb `OpenAndFirstFull` | 6,913,938 | — | — | 6.90–8.28 ms (trial means) | 9,550,912 | 118 |
| Xvfb `WarmFull` | 8,236,400 | 7.54 ms | 11.19 ms | 4.28–15.25 ms | 9,447,304 | 39 |
| Xvfb `WarmRegion256` | 1,207,819 | 1.20 ms | 1.74 ms | 0.41–1.99 ms | 796,552 | 39 |
| Xvfb `WarmRegionLarge` | 7,541,666 | 8.40 ms | 11.22 ms | 4.60–11.70 ms | 8,857,480 | 39 |
| Sway `WarmFull` | 15,865,135 | 16.38 ms | 17.19 ms | 12.65–19.70 ms | 3,152,152 | 92 |
| Sway `WarmRegion256` | 15,810,737 | 16.05 ms | 16.58 ms | 14.76–16.94 ms | 268,557 | 92 |
| Sway `WarmRegionLarge` | 15,724,487 | 16.32 ms | 16.96 ms | 10.94–20.90 ms | 2,955,544 | 92 |
| Sway `SessionFacadeWarmFull` | 16,263,828 | 15.91 ms | 18.16 ms | 11.38–19.15 ms | 3,152,829 | 100 |
| Sway `FirstFull` | 22,723,386 | — | — | 22.22–23.32 ms (trial means) | 3,184,432 | 528 |
| Sway `OpenAndFirstFull` | 335,166,686 | — | — | 334.33–335.50 ms (trial means) | 3,510,754 | 3,009 |

X11 `BackendOpen` times only `NewX11Backend`; Xvfb startup and backend close are
outside the timer. `FirstFull` opens the backend outside the timed sample, while
`OpenAndFirstFull` includes construction and the first `Grab`. The X11 full
output was 1024×768. Region requests of 256×256 and 1024×720 returned bounds of
(0,0)–(256,256) and (0,0)–(1024,720), respectively. The Sway full output was
1024×768; its 256×256 and 1024×720 requests returned those same region
dimensions. Sway `FirstFull` excludes session startup; its `OpenAndFirstFull`
includes managed headless session startup and the first backend `Grab`, with
cleanup outside the timer.

No attended GNOME, KDE, or portal measurements were run, so prompt and consent
behavior remains unknown. The full `just desktop-benchmark` wrapper did not
complete on this host: its unrelated AT-SPI benchmark could not register an
event family. The targeted X11 and Sway capture benchmarks above passed; the
wrapper's baseline regression check therefore remains unverified here. These
results describe only this host and these private compositor fixtures. Do not
treat them as polling recommendations or as a cross-machine regression
baseline.

Never add screenshot files or raw pixel payloads to benchmark artifacts.
