## pf session start

Start a headless sway session and print env vars

### Synopsis

Start a new isolated headless sway session (dbus, sway, wl-paste)
and print the environment variables needed to connect to it.

The session remains foreground until interrupted or stopped by name.
Start in one terminal: pf session start --name example
In another: eval "$(pf session attach example)"
Then run desktop commands or pf session stop example.
Named sessions are same-user only. Use pf session recover NAME for a
stale reservation after its owner exits, including interrupted publication;
this removes only validated registry artifacts, not desktop processes.
Use session cleanup for orphaned runtimes.

```
pf session start [flags]
```

### Options

```
  -h, --help                 help for start
      --name string          reserve a named foreground session for same-user attach and stop
      --res-x int            horizontal resolution (default 1024)
      --res-y int            vertical resolution (default 768)
      --sway-config string   path to custom sway config (default: embedded)
```

### Options inherited from parent commands

```
      --input-backend string        input backend: auto or remote-desktop (authorized Wayland portal/EIS) (default "auto")
      --nested                      start and target a new nested Wayland session
      --remember-input-permission   request persistent authorization for remote-desktop input
      --sync                        sync after observable mutating commands when supported
      --trace-actions               print each API action to stderr as it runs
      --trace-delay duration        sleep after each traced action
```

### SEE ALSO

* [pf session](pf_session.md)	 - Session diagnostics and utilities

