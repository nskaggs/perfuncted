## pf screen grab

Capture a screen region and save as PNG

```
pf screen grab [flags]
```

### Options

```
  -h, --help          help for grab
      --out string    PNG output path or '-' for stdout (Flatpak defaults to stdout when redirected; native defaults to /tmp/pf-grab.png)
      --rect string   x0,y0,x1,y1 (default "0,0,1920,1080")
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

* [pf screen](pf_screen.md)	 - Screen capture operations

