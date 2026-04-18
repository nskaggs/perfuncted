## pf find wait-with-tolerance

Wait for a target hash within a radius tolerance

```
pf find wait-with-tolerance [flags]
```

### Options

```
      --hash string      target hash (decimal or 0xhex) (required)
  -h, --help             help for wait-with-tolerance
      --poll string      poll interval (default "50ms")
      --radius int       pixel radius tolerance (default 1) (default 1)
      --rect string      x0,y0,x1,y1 (default 0,0,100,100) (default "0,0,100,100")
      --timeout string   timeout duration (default "5s")
```

### Options inherited from parent commands

```
      --max-x int32   input coordinate space width (default 1920)
      --max-y int32   input coordinate space height (default 1080)
      --nested        auto-detect and connect to a nested Wayland session in /tmp
```

### SEE ALSO

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

