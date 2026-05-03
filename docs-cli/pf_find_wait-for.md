## pf find wait-for

Wait until a region's pixel hash equals the provided hash

```
pf find wait-for [flags]
```

### Options

```
      --hash string      target hash (decimal or 0xhex)
  -h, --help             help for wait-for
      --poll string      poll interval (default "50ms")
      --rect string      x0,y0,x1,y1 (default "0,0,100,100")
      --timeout string   timeout duration (default "5s")
```

### Options inherited from parent commands

```
      --max-x int32            input coordinate space width (default 1920)
      --max-y int32            input coordinate space height (default 1080)
      --nested                 auto-detect and connect to a nested Wayland session in /tmp
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

