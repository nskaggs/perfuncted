## pf window move

Move a window to absolute screen coordinates

```
pf window move [flags]
```

### Options

```
  -h, --help           help for move
      --title string   window title substring (required)
      --x string       x coordinate or keep (default "keep")
      --y string       y coordinate or keep (default "keep")
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

