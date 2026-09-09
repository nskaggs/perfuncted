## pf accessibility focus

Resolve one semantic node and request AT-SPI focus

```
pf accessibility focus [flags]
```

### Options

```
      --allow-sensitive         include protected text and values
      --app string              accessible application name substring
      --attribute stringArray   required attribute key=value (repeatable)
  -h, --help                    help for focus
      --json                    write machine-readable JSON
      --max-depth int           maximum tree depth
      --max-nodes int           maximum nodes
      --max-text-bytes int      maximum UTF-8 text bytes per node
      --max-total-bytes int     hard maximum serialized snapshot bytes
      --name string             accessible name substring
      --pid int32               exact application process ID
      --role string             accessible role substring
      --state stringArray       required accessible state (repeatable)
      --text string             accessible text substring
      --visible-only            exclude invisible/off-screen nodes
      --window string           exact managed window title
      --window-id string        managed window ID
```

### Options inherited from parent commands

```
      --nested                 start and target a new nested Wayland session
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf accessibility](pf_accessibility.md)	 - Inspect and operate the AT-SPI accessibility tree

