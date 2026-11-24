## pf workflow

Control an active same-user script

### Synopsis

Control a script started with pf run --workflow-id ID.

The control socket and status record live below /tmp/pf-workflows-<uid> and
are private to the current user. Pause is cooperative: status first reports
pause-requested and becomes paused only after the current command reaches the
next script boundary. Stop cancels in-flight context-aware work and waits up to
five seconds for the run and Session cleanup; if it is still stopping, query
status again. Status JSON includes the stable ID, state, last completed step,
current step/phase, and a sanitized terminal error category. It never includes
script contents or typed input. A terminal stopped/failed/completed state can
be read after the socket closes. A process crash has no trustworthy terminal
status, and an unknown or stale endpoint fails closed.

These controls do not reverse operations already delivered to the desktop and
do not provide a global hotkey or process supervisor.

### Options

```
  -h, --help   help for workflow
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

* [pf](pf.md)	 - perfuncted — screen automation CLI
* [pf workflow pause](pf_workflow_pause.md)	 - pause an active workflow
* [pf workflow resume](pf_workflow_resume.md)	 - resume an active workflow
* [pf workflow status](pf_workflow_status.md)	 - show the status of an active workflow
* [pf workflow stop](pf_workflow_stop.md)	 - stop an active workflow

