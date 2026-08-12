## pf session cleanup

Safely clean stale managed session runtimes

### Synopsis

Clean stale Perfuncted runtime directories through the library's
ownership-aware cleanup path. Live owner processes are retained, and recorded
child process IDs are only terminated when their XDG runtime directory matches
the stale session being removed. Dead owner PIDs are reaped immediately;
missing owner files retain a five-minute creation grace, while --max-age
governs malformed owner metadata.

```
pf session cleanup [flags]
```

### Options

```
  -h, --help               help for cleanup
      --max-age duration   age threshold for malformed owner metadata (minimum 5m) (default 24h0m0s)
```

### Options inherited from parent commands

```
      --nested                 start and target a new nested Wayland session
      --sync                   sync after observable mutating commands when supported
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf session](pf_session.md)	 - Session diagnostics and utilities

