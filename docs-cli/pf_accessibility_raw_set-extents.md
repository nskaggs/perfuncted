## pf accessibility raw set-extents

Invoke Component SetExtents

```
pf accessibility raw set-extents [flags]
```

### Options

```
      --bus string                AT-SPI object bus name
      --coordinate-space string   coordinate space: screen, window, or parent (default "screen")
      --generation uint           current accessibility generation
      --height int                component height
  -h, --help                      help for set-extents
      --json                      write machine-readable JSON
      --path string               AT-SPI object path
      --width int                 component width
      --x int                     position x coordinate
      --y int                     position y coordinate
```

### Options inherited from parent commands

```
      --nested                 start and target a new nested Wayland session
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf accessibility raw](pf_accessibility_raw.md)	 - Use typed AT-SPI protocol primitives with explicit handles

