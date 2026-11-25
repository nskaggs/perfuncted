## pf input drag-and-drop

Drag from one coordinate to another (press, move, release)

```
pf input drag-and-drop [flags]
```

### Options

```
      --curve float         perpendicular curve fraction (-0.5..0.5)
      --duration string     motion duration (0 means direct; max 10s) (default "0")
      --easing string       linear or ease-in-out (default "linear")
  -h, --help[=true|false]   help for drag-and-drop
      --seed uint           variation seed
      --variation int       seeded coordinate variation in units (0..16)
      --x1 int              start x
      --x2 int              end x
      --y1 int              start y
      --y2 int              end y
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

* [pf input](pf_input.md)	 - Mouse and keyboard injection

