## pf find color

Find the first pixel matching a colour within tolerance

```
pf find color [flags]
```

### Options

```
      --color string    target colour as RRGGBB hex (required)
  -h, --help            help for color
      --rect string     search area x0,y0,x1,y1 (default "0,0,1920,1080")
      --tolerance int   per-channel tolerance (0-255)
```

### Options inherited from parent commands

```
      --max-x int32   input coordinate space width (default 1920)
      --max-y int32   input coordinate space height (default 1080)
      --nested        auto-detect and connect to a nested Wayland session in /tmp
```

### SEE ALSO

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

