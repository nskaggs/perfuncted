## pf find color

Find the first pixel matching a colour within tolerance

```
pf find color [flags]
```

### Options

```
      --color string        target colour as RRGGBB hex (required)
  -h, --help[=true|false]   help for color
      --rect string         search area x0,y0,x1,y1 (default "0,0,1920,1080")
      --tolerance int       per-channel tolerance (0-255)
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

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

