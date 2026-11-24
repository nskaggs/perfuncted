## pf workflow stop

stop an active workflow

```
pf workflow stop ID [flags]
```

### Examples

```
  pf workflow stop task-1
```

### Options

```
  -h, --help            help for stop
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

* [pf workflow](pf_workflow.md)	 - Control an active same-user script

