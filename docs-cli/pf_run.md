## pf run

Run CLI commands from a script file or stdin

### Synopsis

Run CLI commands from a script file or stdin.

Use --workflow-id to expose same-user pause, resume, stop, and status controls:

  pf run task.pf --workflow-id task-1
  pf workflow pause task-1
  pf workflow status task-1 --output json
  pf workflow resume task-1
  pf workflow stop task-1

Pause takes effect between script commands; a command already in progress may
finish before status becomes paused. Stop cancels the run context and closes
its Session so owned input cleanup can finish. Stop cannot undo application
changes that already happened. Controlled runs require a script file, reject
input type --stdin and explicit key-down/key-up syntax; atomic key chords remain
available. A crash can make the live state unavailable until the stale private
control record is safely recovered by starting another run with the same ID.

```
pf run FILE [flags]
```

### Options

```
  -h, --help[=true|false]    help for run
      --workflow-id string   explicit ID for same-user workflow control
```

### Options inherited from parent commands

```
      --input-backend string                     input backend: auto or remote-desktop (authorized Wayland portal/EIS) (default "auto")
      --nested[=true|false]                      start and target a new nested Wayland session
      --remember-input-permission[=true|false]   request persistent authorization for remote-desktop input
      --sync[=true|false]                        sync after observable mutating commands when supported
      --trace-actions[=true|false]               print each API action to stderr as it runs
      --trace-delay duration                     sleep after each traced action
```

### SEE ALSO

* [pf](pf.md)	 - perfuncted — screen automation CLI

