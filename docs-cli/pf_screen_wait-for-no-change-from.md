## pf screen wait-for-no-change-from

Wait for a screen region to stop changing, starting from an initial hash

```
pf screen wait-for-no-change-from [flags]
```

### Options

```
  -h, --help             help for wait-for-no-change-from
      --initial string   initial CRC32 hash (decimal or 0xhex)
      --poll string      polling interval (e.g. 200ms)
      --rect string      region to monitor as x0,y0,x1,y1 (default "0,0,1920,1080")
      --stable int       number of stable samples required
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

