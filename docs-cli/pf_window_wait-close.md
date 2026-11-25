## pf window wait-close

Wait until matching windows disappear

```
pf window wait-close [match-spec ...] [flags]
```

### Options

```
  -h, --help[=true|false]   help for wait-close
      --poll string         poll interval (default "100ms")
      --timeout string      timeout duration (default "5s")
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

