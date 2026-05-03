## pf session start

Start a headless sway session and print env vars

### Synopsis

Start a new isolated headless sway session (dbus, sway, wl-paste)
and print the environment variables needed to connect to it.

The session runs until this process is interrupted (Ctrl+C) or killed.
Use the printed env vars in another terminal to connect:

  eval $(pf session start)
  kwrite /tmp/test.txt &
  pf screen grab --out /tmp/shot.png

```
pf session start [flags]
```

### Options

```
  -h, --help                 help for start
      --res-x int            horizontal resolution (default 1024)
      --res-y int            vertical resolution (default 768)
      --sway-config string   path to custom sway config (default: embedded)
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

* [pf session](pf_session.md)	 - Session diagnostics and utilities

