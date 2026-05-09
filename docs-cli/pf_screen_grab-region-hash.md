## pf screen grab-region-hash

Print the CRC32 hash of a screen region

### Synopsis

Captures the specified screen region and prints its CRC32 pixel hash as a
zero-padded hex integer. Useful for polling whether a region has changed.

```
pf screen grab-region-hash [flags]
```

### Options

```
  -h, --help          help for grab-region-hash
      --rect string   region to hash as x0,y0,x1,y1 (default "0,0,1920,1080")
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

