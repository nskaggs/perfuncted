## pf screen get-multiple-pixels

Capture several pixels in one screen grab

### Synopsis

Captures a bounding rectangle covering every requested point, then prints the
colour of each point in order. Use --output json for machine parsing, or the
plain format for quick interactive checks.

```
pf screen get-multiple-pixels [flags]
```

### Options

```
  -h, --help[=true|false]   help for get-multiple-pixels
      --output string       plain|json (default "plain")
      --points string       semicolon-separated x,y pairs
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

* [pf screen](pf_screen.md)	 - Screen capture operations

