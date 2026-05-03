## pf find scan-for

Scan multiple regions until one matches its expected hash

```
pf find scan-for [flags]
```

### Options

```
  -h, --help             help for scan-for
      --poll string      poll interval (default "50ms")
      --rects string     semicolon-separated rects: x0,y0,x1,y1;...
      --timeout string   timeout duration (default "5s")
      --wants string     comma-separated expected hashes
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

