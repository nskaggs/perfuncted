## pf find image

Find the best fuzzy match for a PNG template

### Synopsis

Search a screen region for a PNG template and print its desktop position and
similarity score. The score is one minus the reference-alpha-weighted mean absolute
RGB channel difference, normalized to [0,1]; it is a similarity measure, not a
probability. Transparent template pixels are ignored and partial alpha weights a
pixel. Matching does not compensate for scale or rotation. For exact pixel matching,
the Go API provides find.LocateExact. The PNG must be no larger than 32 MiB and 2
million pixels, with neither dimension above 8192 pixels. The active capture backend
must verify that it captured the complete requested region.

```
pf find image [flags]
```

### Options

```
      --confidence float   minimum similarity score (0..1) (default 0.95)
  -h, --help               help for image
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

