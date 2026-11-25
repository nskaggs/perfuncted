## pf input move

Move mouse to absolute coordinates

```
pf input move [flags]
```

### Options

```
      --curve float         perpendicular curve fraction (-0.5..0.5)
      --duration string     motion duration (0 means direct; max 10s) (default "0")
      --easing string       linear or ease-in-out (default "linear")
      --from-x int          explicit start x (required with from-y)
      --from-y int          explicit start y (required with from-x)
  -h, --help[=true|false]   help for move
      --seed uint           variation seed
      --variation int       seeded coordinate variation in units (0..16)
      --x int               x coordinate
      --y int               y coordinate
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

