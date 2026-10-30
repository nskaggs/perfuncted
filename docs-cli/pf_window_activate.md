## pf window activate

Bring a window to the foreground by title substring (case-insensitive)

```
pf window activate <pattern> [flags]
```

### Options

```
  -h, --help                      help for activate
      --verify                    verify actual window focus
      --verify-poll duration      verification polling interval (default 50ms)
      --verify-timeout duration   maximum time to verify focus (default 2s)
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

