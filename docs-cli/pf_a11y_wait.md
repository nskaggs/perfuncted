## pf a11y wait

Wait for a scoped accessible control state

### Synopsis

Wait for one uniquely matched accessible control using fresh complete snapshots. Notifications only wake the wait; they do not establish success. --state filters initial target selection, while --until names the state to wait for; after the first match, the wait follows that node identity. --timeout is required. --visible-only cannot be used to prove absence or a hidden state.

```
pf a11y wait [flags]
```

### Options

```
      --allow-sensitive         include protected text and values
      --app string              accessible application name substring
      --attribute stringArray   required attribute key=value (repeatable)
  -h, --help                    help for wait
      --json                    write machine-readable JSON
      --max-depth int           maximum tree depth
      --max-nodes int           maximum nodes
      --max-text-bytes int      maximum UTF-8 text bytes per node
      --max-total-bytes int     hard maximum serialized snapshot bytes
      --name string             accessible name substring
      --pid int32               exact application process ID
      --poll duration           fallback polling interval (defaults to the session interval)
      --role string             accessible role substring
      --state stringArray       required accessible state (repeatable)
      --text string             accessible text substring
      --timeout duration        required maximum wait duration (e.g. 5s)
      --until string            state to wait for: present, absent, focused, unfocused, visible, hidden, enabled, disabled, value-changed (default "present")
      --visible-only            exclude invisible/off-screen nodes
      --window string           exact managed window title
      --window-id string        managed window ID
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

* [pf a11y](pf_a11y.md)	 - Inspect and operate the AT-SPI accessibility tree

