## pf input click-window

Click a window-relative logical point using an explicitly declared backend coordinate space

```
pf input click-window [flags]
```

### Options

```
      --button int     mouse button (default 1)
  -h, --help           help for click-window
      --id string      stable ID from window list
      --title string   unique window title substring
      --x int          logical window-relative X (default -1)
      --y int          logical window-relative Y (default -1)
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

