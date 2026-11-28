## pf window activate-id

Activate a specific window even when titles are duplicated

```
pf window activate-id <stable-id> [flags]
```

### Options

```
  -h, --help[=true|false]         help for activate-id
      --verify[=true|false]       wait for actual target focus confirmation (default true)
      --verify-poll duration      focus verification poll interval (default 50ms)
      --verify-timeout duration   maximum wait for target to become active (default 2s)
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

* [pf window](pf_window.md)	 - Window management

