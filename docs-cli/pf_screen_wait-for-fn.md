## pf screen wait-for-fn

Wait for a screen region to satisfy a built-in predicate

### Synopsis

Uses the library's WaitForFn helper with a small set of practical built-in
predicates. This is useful when the caller wants a higher-level readiness
check than a raw hash comparison.

```
pf screen wait-for-fn [flags]
```

### Options

```
  -h, --help               help for wait-for-fn
      --poll string        poll interval (default "50ms")
      --predicate string   built-in predicate: non-empty|opaque|non-zero (default "non-empty")
      --rect string        x0,y0,x1,y1 (default "0,0,100,100")
      --timeout string     timeout duration (default "5s")
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

