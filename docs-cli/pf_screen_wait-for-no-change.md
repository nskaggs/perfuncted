## pf screen wait-for-no-change

Wait for a screen region to stop changing

```
pf screen wait-for-no-change [flags]
```

### Options

```
  -h, --help          help for wait-for-no-change
      --poll string   polling interval (e.g. 200ms)
      --rect string   region to monitor as x0,y0,x1,y1 (default "0,0,1920,1080")
      --stable int    number of stable samples required
```

### Options inherited from parent commands

```
      --nested                 start and target a new nested Wayland session
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf screen](pf_screen.md)	 - Screen capture operations

