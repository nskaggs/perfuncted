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
      --nested                 start and target a new nested Wayland session
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

