## pf find wait-for-visible-change

Wait until a region's visible content changes (useful for animations/loads)

```
pf find wait-for-visible-change [flags]
```

### Options

```
      --rect string      x0,y0,x1,y1 (default "0,0,100,100")
      --timeout string   timeout duration (default "5s")
      --poll string      poll interval (default "50ms")
  -h, --help             help for wait-for-visible-change
```

### Options inherited from parent commands

```
      --max-x int32   input coordinate space width (default 1920)
      --max-y int32   input coordinate space height (default 1080)
      --nested        auto-detect and connect to a nested Wayland session in /tmp
```

### SEE ALSO

* [pf find wait-for](pf_find_wait-for.md)
