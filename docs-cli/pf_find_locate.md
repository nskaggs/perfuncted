## pf find locate

Find a reference PNG image within a screen region

### Synopsis

Scans searchArea for an exact pixel match of the reference image and prints the bounding rectangle of the first match.

```
pf find locate [flags]
```

### Options

```
  -h, --help          help for locate
      --rect string   search area x0,y0,x1,y1 (default "0,0,1920,1080")
      --ref string    path to reference PNG image
```

### Options inherited from parent commands

```
      --max-x int32   input coordinate space width (default 1920)
      --max-y int32   input coordinate space height (default 1080)
      --nested        auto-detect and connect to a nested Wayland session in /tmp
```

### SEE ALSO

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

