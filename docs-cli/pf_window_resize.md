## pf window resize

Resize a window

```
pf window resize [flags]
```

### Options

```
      --h int               height in pixels (default 600)
  -h, --help[=true|false]   help for resize
      --id string           stable window ID from window list (instead of --title)
      --title string        unique case-insensitive title substring (required unless --id)
      --w int               width in pixels (default 800)
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

