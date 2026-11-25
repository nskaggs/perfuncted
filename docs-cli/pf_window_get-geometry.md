## pf window get-geometry

Print reported window geometry

### Synopsis

Select one window by unique case-insensitive title substring or --id. Plain output is x0,y0,x1,y1; JSON uses x,y,w,h in compositor-reported coordinates. For --id, JSON title is the reported window title.

```
pf window get-geometry [title] [flags]
```

### Options

```
  -h, --help[=true|false]   help for get-geometry
      --id string           stable window ID from window list (instead of positional title)
      --output string       plain|json (default "plain")
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

