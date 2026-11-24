## pf session recover

Manage named foreground headless sessions: recover

### Synopsis

Remove only known registry artifacts from a stale named reservation after obtaining its exclusive owner lock. Unpublished reservations may contain partial temporary metadata; published records must have a verified absent owner or a reused PID with a different valid start time. Unsafe or uncertain identities are refused. No process or desktop runtime is removed.

```
pf session recover NAME [flags]
```

### Options

```
  -h, --help            help for recover
      --output string   plain|json (default "plain")
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

