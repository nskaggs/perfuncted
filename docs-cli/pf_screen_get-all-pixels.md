## pf screen get-all-pixels

Capture the entire screen and output raw RGBA pixels to stdout

### Synopsis

Captures the entire screen and writes the raw 8-bit RGBA pixel data
directly to stdout. Useful for piping into ffmpeg, imagemagick, or other tools.

```
pf screen get-all-pixels [flags]
```

### Options

```
  -h, --help[=true|false]   help for get-all-pixels
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

