## pf screen watch

Continuously print hash changes in a screen region

### Synopsis

Polls a screen region and prints a timestamped line whenever the pixel hash
changes. Useful for tuning poll intervals, spotting oscillating regions, and
understanding which parts of the screen change during an action.

Output format:
  <timestamp>  <hash>  <label>

Runs until --duration expires or Ctrl+C.

```
pf screen watch [flags]
```

### Options

```
      --duration string   stop after this duration (e.g. 10s); default runs until Ctrl+C
  -h, --help              help for watch
      --poll string       poll interval (default "100ms")
      --rect string       x0,y0,x1,y1 region to monitor (default "0,0,1920,1080")
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

* [pf screen](pf_screen.md)	 - Screen capture operations

