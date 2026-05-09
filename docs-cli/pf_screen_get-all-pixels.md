## pf screen get-all-pixels

Capture the entire screen and output raw RGBA pixels to stdout

### Synopsis

Captures the entire screen and writes the raw 8-bit RGBA pixel data
directly to stdout. Useful for piping into ffmpeg, imagemagick, or other tools.

```
pf screen get-all-pixels [flags]
```

### Options

```
  -h, --help   help for get-all-pixels
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

