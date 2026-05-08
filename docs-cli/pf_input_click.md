## pf input click

Click a mouse button at coordinates

```
pf input click [flags]
```

### Options

```
      --button int     1=left 2=middle 3=right (default 1)
      --delay string   delay between clicks (default "0")
  -h, --help           help for click
      --repeat int     repeat count (default 1)
      --x int          x coordinate
      --y int          y coordinate
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

* [pf input](pf_input.md)	 - Mouse and keyboard injection

