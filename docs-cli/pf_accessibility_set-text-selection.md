## pf accessibility set-text-selection

Set a range through AT-SPI Text

```
pf accessibility set-text-selection [flags]
```

### Options

```
      --allow-sensitive       include sensitive/protected text (use with care)
      --app string            application accessible-name substring
      --application string    application accessible-name substring (alias for --app)
      --desktop-root          explicitly allow bounded whole-desktop traversal
      --end int32             selection end offset
      --generation uint       current accessibility generation for --root-bus/--root-path
  -h, --help                  help for set-text-selection
      --json                  output JSON (alias for --output json)
      --max-depth int         maximum tree depth
      --max-nodes int         maximum nodes
      --max-text-bytes int    maximum text bytes per node
      --output string         output format (json) (default "json")
      --pid int32             application process ID
      --root-bus string       AT-SPI application bus name
      --root-path string      AT-SPI application object path
      --selection int32       selection number
      --start int32           selection start offset
      --visible-only          exclude invisible/off-screen nodes
      --window-id string      managed window identifier
      --window-title string   managed window title (exact)
```

### Options inherited from parent commands

```
      --nested                 start and target a new nested Wayland session
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf accessibility](pf_accessibility.md)	 - Inspect the AT-SPI accessibility tree

