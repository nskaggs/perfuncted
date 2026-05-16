## pf find wait-for-visible-change

Wait until a region's visible content changes (useful for animations/loads)

```
pf find wait-for-visible-change [flags]
```

### Options

```
  -h, --help             help for wait-for-visible-change
      --poll string      poll interval (default "50ms")
      --rect string      x0,y0,x1,y1 (default "0,0,100,100")
      --stable int       consecutive identical samples required (default 3)
      --timeout string   timeout duration (default "5s")
```

### Options inherited from parent commands

```
      --max-x int32            input coordinate space width (default 1920)
      --max-y int32            input coordinate space height (default 1080)
      --nested                 auto-detect and connect to a nested Wayland session in /tmp
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

