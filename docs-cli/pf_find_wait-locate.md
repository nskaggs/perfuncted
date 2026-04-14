## pf find wait-locate

Poll until a reference image is found in the search area

```
pf find wait-locate [flags]
```

### Options

```
  -h, --help             help for wait-locate
      --poll string      poll interval (default "200ms")
      --rect string      search area x0,y0,x1,y1 (default "0,0,1920,1080")
      --ref string       reference PNG image path (required)
      --timeout string   maximum wait time (default "10s")
```

### Options inherited from parent commands

```
      --max-x int32   input coordinate space width (default 1920)
      --max-y int32   input coordinate space height (default 1080)
      --nested        auto-detect and connect to a nested Wayland session in /tmp
```

### SEE ALSO

* [pf find](pf_find.md)	 - Pixel scanning and wait utilities

