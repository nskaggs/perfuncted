## pf window move

Move a window to absolute screen coordinates

```
pf window move [flags]
```

### Options

```
  -h, --help           help for move
      --title string   window title substring (required)
      --x int          x coordinate
      --y int          y coordinate
```

### Options inherited from parent commands

```
      --max-x int32            input coordinate space width (default 1920)
      --max-y int32            input coordinate space height (default 1080)
      --nested                 auto-detect and connect to a nested Wayland session in /tmp
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf window](pf_window.md)	 - Window management

