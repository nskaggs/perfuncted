## pf window wait-for

Wait until a matching window appears

```
pf window wait-for [match-spec ...] [flags]
```

### Options

```
  -h, --help             help for wait-for
      --poll string      poll interval (default "100ms")
      --timeout string   timeout duration (default "5s")
```

### Options inherited from parent commands

```
      --input-backend string        input backend: auto or remote-desktop (authorized Wayland portal/EIS) (default "auto")
      --nested                      start and target a new nested Wayland session
      --remember-input-permission   request persistent authorization for remote-desktop input
      --sync                        sync after observable mutating commands when supported
      --trace-actions               print each API action to stderr as it runs
      --trace-delay duration        sleep after each traced action
```

### SEE ALSO

* [pf window](pf_window.md)	 - Window management

