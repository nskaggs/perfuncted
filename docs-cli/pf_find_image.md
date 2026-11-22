## pf find image

Find fuzzy matches for a PNG template

### Synopsis

Search a screen region for a PNG template and print its desktop position and
similarity score. The score is one minus the reference-alpha-weighted mean absolute
RGB channel difference, normalized to [0,1]; it is a similarity measure, not a
probability. Transparent template pixels are ignored and partial alpha weights a
pixel. Matching does not compensate for scale or rotation. For exact pixel matching,
the Go API provides find.LocateExact. The PNG must be no larger than 32 MiB and 2
million pixels, with neither dimension above 8192 pixels. The active capture backend
must verify that it captured the complete requested region.
With --all, print up to --max-results non-overlapping matches (default 64)
in descending score order, ties top-to-bottom then left-to-right. Overlapping
candidates are greedily suppressed by earlier matches. Each match uses the
same output line format as best-match mode. No matches prints no lines.
The limit bounds output and candidate retention, but the exact greedy ordering
may require more than --max-results candidates in memory. In the worst allowed
16-Mpixel capture, this can require up to 256 MiB of additional retained heap
storage plus allocation overhead. The full search is scanned once.

```
pf find image [flags]
```

### Examples

```
pf find image --template button.png --rect 0,0,1280,720 --confidence 0.9
pf find image --template button.png --rect 0,0,1280,720 --confidence 0.9 --all --max-results 10
```

### Options

```
      --all                return non-overlapping matches in score order
      --confidence float   minimum similarity score (0..1) (default 0.95)
  -h, --help               help for image
      --max-results int    maximum matches with --all (1..64) (default 64)
      --rect string        search region as x0,y0,x1,y1
      --template string    PNG template image to find
      --timeout string     capture and matching timeout (default "5s")
```

### Options inherited from parent commands

```
      --input-backend string        input backend: auto or remote-desktop (authorized Wayland portal/EIS) (default "auto")
      --nested                      start and target a new nested Wayland session
      --remember-input-permission   request persistent authorization for remote-desktop input
      --sync                        sync after observable mutating commands when supported
      --trace-actions               print each API action to stderr as it runs
      --trace-delay duration        sleep after each traced action
```

### SEE ALSO

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

