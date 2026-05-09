## pf window wait-for-close

Block until a window matching the title pattern closes

### Synopsis

Polls window titles at the given interval until no window whose title contains
--pattern is found. Exits cleanly when the window is gone, or returns an error
if the context deadline is exceeded.

```
pf window wait-for-close [flags]
```

### Options

```
  -h, --help             help for wait-for-close
      --pattern string   substring to match against window titles (case-insensitive)
      --poll string      polling interval (e.g. 200ms); leave empty to use the default
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

* [pf window](pf_window.md)	 - Window management

