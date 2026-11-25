## pf screen grab-full-hash

Print the CRC32 hash of the full screen contents

### Synopsis

Captures the full screen and prints the CRC32 pixel hash as a zero-padded
hex integer. Useful as a quick change-detection probe without saving any files.

```
pf screen grab-full-hash [flags]
```

### Options

```
  -h, --help[=true|false]   help for grab-full-hash
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

