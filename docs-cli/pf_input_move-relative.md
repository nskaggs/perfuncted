## pf input move-relative

Move pointer relative to its compositor-reported position

```
pf input move-relative [flags]
```

### Options

```
      --dx int   horizontal delta in compositor-logical pixels
      --dy int   vertical delta in compositor-logical pixels
  -h, --help     help for move-relative
      --verify   confirm compositor cursor reached the target (default true)
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

* [pf input](pf_input.md)	 - Mouse and keyboard injection

