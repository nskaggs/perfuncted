## pf screen wait-for-no-change-from

Wait for a screen region to stop changing, starting from an initial hash

```
pf screen wait-for-no-change-from [flags]
```

### Options

```
  -h, --help[=true|false]   help for wait-for-no-change-from
      --initial string      initial CRC32 hash (decimal or 0xhex)
      --poll string         polling interval (e.g. 200ms)
      --rect string         region to monitor as x0,y0,x1,y1 (default "0,0,1920,1080")
      --stable int          number of stable samples required
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

* [pf screen](pf_screen.md)	 - Screen capture operations

