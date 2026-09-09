## pf a11y raw scroll

Invoke the low-level AT-SPI Component ScrollTo primitive

```
pf a11y raw scroll [flags]
```

### Options

```
      --alignment string   alignment: top-left, bottom-right, top-edge, bottom-edge, left-edge, right-edge, or anywhere (default "anywhere")
      --bus string         AT-SPI object bus name
      --generation uint    current accessibility generation
  -h, --help               help for scroll
      --json               write machine-readable JSON
      --path string        AT-SPI object path
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

