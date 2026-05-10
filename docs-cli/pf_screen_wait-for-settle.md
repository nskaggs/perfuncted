## pf screen wait-for-settle

Wait for a screen region to change and then settle

### Synopsis

Captures a baseline hash, yields to the caller's surrounding action model,
then waits for the region to change and become stable again. The CLI version
uses a no-op action so it still works as a pure readiness probe.

```
pf screen wait-for-settle [flags]
```

### Options

```
  -h, --help             help for wait-for-settle
      --poll string      poll interval (default "100ms")
      --rect string      x0,y0,x1,y1 (default "0,0,100,100")
      --stable string    consecutive identical samples required (default "3")
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

* [pf screen](pf_screen.md)	 - Screen capture operations

