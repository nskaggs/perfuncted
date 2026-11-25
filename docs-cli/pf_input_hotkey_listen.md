## pf input hotkey listen

Listen for an authorized global shortcut or mouse-button event

```
pf input hotkey listen [flags]
```

### Options

```
      --button string         mouse button (left, middle, right; native X11 only)
      --combo string          named shortcut (for example F8 or Ctrl+Shift+P)
      --event string          press, release, or both (default "press")
  -h, --help[=true|false]     help for listen
      --output string         output format: json (default "json")
      --repeat[=true|false]   report backend-attested repeat events when supported
      --timeout duration      maximum listening duration (0 means until interrupted)
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

* [pf input hotkey](pf_input_hotkey.md)	 - Observe authorized global shortcuts and mouse buttons

