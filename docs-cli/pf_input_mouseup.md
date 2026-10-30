## pf input mouseup

Release a mouse button (optional coords)

```
pf input mouseup [flags]
```

### Options

```
      --button int   button number (default 1)
  -h, --help         help for mouseup
      --x int        x coordinate (optional) (default -1)
      --y int        y coordinate (optional) (default -1)
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

* [pf input](pf_input.md)	 - Mouse and keyboard injection

