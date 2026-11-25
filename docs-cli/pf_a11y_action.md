## pf a11y action

Invoke one semantic AT-SPI action and optionally verify its result

### Synopsis

Invoke one uniquely resolved semantic AT-SPI action.

Optional --expect-text or --expect-state changed waits for fresh accessible
state with a bounded timeout. A verified result means the condition was
observed; it does not prove the action caused it. A timeout never retries the
action. Without an expectation, the existing one-shot receipt is returned.

Examples:
  pf a11y action --app Editor --role button --name Save --expect-text Saved --timeout 5s
  pf a11y action --app Editor --role button --name Save --expect-state changed --timeout 5s

```
pf a11y action [flags]
```

### Options

```
      --action string                  exact action name; empty selects the provider's first action
      --allow-sensitive[=true|false]   include protected text and values
      --app string                     accessible application name substring
      --attribute stringArray          required attribute key=value (repeatable)
      --expect-state string            verify an observable state transition; supported value: changed
      --expect-text string             verify that accessible state exposes this text after the action
  -h, --help[=true|false]              help for action
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
      --timeout duration               maximum time for a verified action (must be positive) (default 5s)
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

