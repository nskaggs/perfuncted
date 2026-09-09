## pf a11y raw scroll-to-point

Invoke Component ScrollToPoint

```
pf a11y raw scroll-to-point [flags]
```

### Options

```
      --bus string                AT-SPI object bus name
      --coordinate-space string   coordinate space: screen, window, or parent (default "screen")
      --generation uint           current accessibility generation
  -h, --help                      help for scroll-to-point
      --json                      write machine-readable JSON
      --path string               AT-SPI object path
      --x int                     point x coordinate
      --y int                     point y coordinate
```

### Options inherited from parent commands

```
      --nested                 start and target a new nested Wayland session
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf a11y raw](pf_a11y_raw.md)	 - Use typed AT-SPI protocol primitives with explicit handles

