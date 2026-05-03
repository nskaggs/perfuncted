## pf find wait-for-no-change

Wait until a region's pixel hash is stable for N consecutive samples

### Synopsis

Polls a screen region until its pixel hash is unchanged for --stable consecutive
samples. Pairs with wait-for-change: use wait-for-change to detect when something
starts (e.g. navigation begins), then wait-for-no-change to detect when it finishes.

```
pf find wait-for-no-change [flags]
```

### Options

```
  -h, --help             help for wait-for-no-change
      --poll string      poll interval (default "200ms")
      --rect string      x0,y0,x1,y1 (default "0,0,100,100")
      --stable int       consecutive identical samples required (default 5)
      --timeout string   timeout duration (default "30s")
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

