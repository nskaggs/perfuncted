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
      --nested                 start and target a new nested Wayland session
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

