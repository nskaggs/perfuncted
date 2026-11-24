## pf input location

Print current pointer location

### Synopsis

Print current pointer location as x,y or JSON. JSON reports fresh GNOME global logical coordinates (1:1) or X11 root framebuffer pixels (native 1:1, not monitor scaling). Signed coordinates and origin zero are valid. observedAt is the local observation receipt time, not an atomic guarantee for subsequent input. Portal/EIS, virtual pointer, uinput and unverified KDE routes return unsupported in JSON; plain output retains its existing fallback.

```
pf input location [flags]
```

### Options

```
  -h, --help            help for location
      --output string   Output format: plain or json (authoritative input provider only) (default "plain")
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

