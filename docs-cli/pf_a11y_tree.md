## pf a11y tree

Capture a bounded tree for one application or managed window

```
pf a11y tree [flags]
```

### Options

```
      --allow-sensitive       include protected text and values
      --app string            accessible application name substring
  -h, --help                  help for tree
      --json                  write machine-readable JSON
      --max-depth int         maximum tree depth
      --max-nodes int         maximum nodes
      --max-text-bytes int    maximum UTF-8 text bytes per node
      --max-total-bytes int   hard maximum serialized snapshot bytes
      --pid int32             exact application process ID
      --visible-only          exclude invisible/off-screen nodes
      --window string         exact managed window title
      --window-id string      managed window ID
```

### Options inherited from parent commands

```
      --nested                 start and target a new nested Wayland session
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf a11y](pf_a11y.md)	 - Inspect and operate the AT-SPI accessibility tree

