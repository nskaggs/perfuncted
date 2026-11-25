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


Publish benchmark results with the commit, runtime, compositor, geometry, and measurement method; omit machine identifiers, raw pixels and unrelated local diagnostics.
