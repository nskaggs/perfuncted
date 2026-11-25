## pf a11y text

Resolve one editable semantic node and replace its text

```
pf a11y text [flags]
```

### Options

```
      --allow-sensitive[=true|false]   include protected text and values
      --app string                     accessible application name substring
      --attribute stringArray          required attribute key=value (repeatable)
  -h, --help[=true|false]              help for text
      --json[=true|false]              write machine-readable JSON
      --max-depth int                  maximum tree depth
      --max-nodes int                  maximum nodes
      --max-text-bytes int             maximum UTF-8 text bytes per node
      --max-total-bytes int            hard maximum serialized snapshot bytes
      --name string                    accessible name substring
      --pid int32                      exact application process ID
      --role string                    accessible role substring
      --state stringArray              required accessible state (repeatable)
      --text string                    accessible text substring
      --value string                   replacement text
      --visible-only[=true|false]      exclude invisible/off-screen nodes
      --window string                  exact managed window title
      --window-id string               managed window ID
```

### Options inherited from parent commands

```
      --input-backend string                     input backend: auto or remote-desktop (authorized Wayland portal/EIS) (default "auto")
      --nested[=true|false]                      start and target a new nested Wayland session
      --remember-input-permission[=true|false]   request persistent authorization for remote-desktop input
      --sync[=true|false]                        sync after observable mutating commands when supported
      --trace-actions[=true|false]               print each API action to stderr as it runs
      --trace-delay duration                     sleep after each traced action
```

### SEE ALSO

* [pf a11y](pf_a11y.md)	 - Inspect and operate the AT-SPI accessibility tree

