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
  -h, --help   help for grab-full-hash
```

### Options inherited from parent commands

```
      --max-x int32            input coordinate space width (default 1920)
      --max-y int32            input coordinate space height (default 1080)
      --nested                 auto-detect and connect to a nested Wayland session in /tmp
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf screen](pf_screen.md)	 - Screen capture operations

