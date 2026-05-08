## pf window wait-for

Wait until a matching window appears

```
pf window wait-for [match-spec ...] [flags]
```

### Options

```
  -h, --help             help for wait-for
      --poll string      poll interval (default "100ms")
      --timeout string   timeout duration (default "5s")
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

