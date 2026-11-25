## pf screen watch

Continuously print hash changes in a screen region

### Synopsis

Polls a screen region and prints a timestamped line whenever the pixel hash
changes. Useful for tuning poll intervals, spotting oscillating regions, and
understanding which parts of the screen change during an action.

Output format:
  <timestamp>  <hash>  <label>

With --changes --output json, emit metadata-only NDJSON from full-frame polling.
The initial frame and resyncs have no base frame; unchanged samples are silent.
Dirty rectangles are desktop bounds, capped at 256 before full-frame resync.
Geometry version starts at 1 and advances only when screen or image bounds change.
Format and rectangle-overflow resyncs retain the geometry version.
Negative poll intervals are rejected in changes mode.
With --include-pixels (requires --changes --output json), pixels contains PNG
payloads in base64, with image-space bounds, dimensions and capture scale.
Full frames contain the entire capture; deltas contain exact changed crops.
Pixel records are limited to 16 MiB serialized (including newline), and PNG
working dimensions to 16 Mi pixels and 16384 pixels per axis. Oversized
records fail before output. No pixels are encoded without explicit opt-in.

Runs until --duration expires or Ctrl+C.

```
pf screen watch [flags]
```

### Options

```
      --changes[=true|false]          emit metadata-only dirty rectangles (requires --output json)
      --duration string               stop after this duration (e.g. 10s); default runs until Ctrl+C
  -h, --help[=true|false]             help for watch
      --include-pixels[=true|false]   include exact PNG crops (requires --changes --output json)
      --output string                 plain|json (default "plain")
      --poll string                   poll interval (default "100ms")
      --rect string                   x0,y0,x1,y1 region to monitor (default "0,0,1920,1080")
```

### Options inherited from parent commands

```
      --input-backend string                     input backend: auto or remote-desktop (authorized Wayland portal/EIS) (default "auto")
      --nested[=true|false]                      start and target a new nested Wayland session
      --remember-input-permission[=true|false]   request persistent authorization for remote-desktop input
      --sync[=true|false]                        sync after observable mutating commands when supported
      --trace-actions[=true|false]               print each API action to stderr as it runs
      --trace-delay duration                     sleep after each traced action
```

### SEE ALSO

* [pf screen](pf_screen.md)	 - Screen capture operations

