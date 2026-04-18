## pf find wait-with-tolerance

Wait for a target hash within a radius tolerance

```
pf find wait-with-tolerance [flags]
```

### Options

```
      --rect string      x0,y0,x1,y1 (default "0,0,100,100")
      --hash string      target hash (decimal or 0xhex)
      --radius int       pixel radius tolerance (default 1)
      --poll string      poll interval (default "50ms")
      --timeout string   timeout duration (default "5s")
  -h, --help             help for wait-with-tolerance
```

### Options inherited from parent commands

```
      --max-x int32   input coordinate space width (default 1920)
      --max-y int32   input coordinate space height (default 1080)
      --nested        auto-detect and connect to a nested Wayland session in /tmp
```

### SEE ALSO

* [pf find wait-for](pf_find_wait-for.md)
