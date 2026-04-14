## pf find wait-for-change

Wait until a region's pixel hash changes from an initial value

```
pf find wait-for-change [flags]
```

### Options

```
      --capture-initial   capture current region hash and wait for it to change
  -h, --help              help for wait-for-change
      --initial string    initial hash (decimal or 0xhex)
      --poll string       poll interval (default "50ms")
      --rect string       x0,y0,x1,y1 (default "0,0,100,100")
      --timeout string    timeout duration (default "5s")
```

### Options inherited from parent commands

```
      --max-x int32   input coordinate space width (default 1920)
      --max-y int32   input coordinate space height (default 1080)
      --nested        auto-detect and connect to a nested Wayland session in /tmp
```

### SEE ALSO

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

