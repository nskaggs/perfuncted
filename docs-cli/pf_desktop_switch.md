## pf desktop switch

Switch to a virtual desktop by stable ID

```
pf desktop switch <desktop-id> [flags]
```

### Options

```
  -h, --help     help for switch
      --verify   confirm current desktop changed (default true)
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

* [pf desktop](pf_desktop.md)	 - Inspect and change compositor virtual desktops

