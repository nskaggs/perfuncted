## pf input state

Query one current key, lock, or mouse-button state

### Synopsis

Query one input state on demand through a read-only observation backend.
On X11, results report aggregate logical state with unknown physical-versus-
synthetic provenance. They do not record key history or infer state from prior
Perfuncted input commands. Other backends return an explicit unsupported error.
This currently requires native X11 with XI2; Wayland and XWayland are unsupported.
It does not request root or input-device access. Caps Lock's lock state is
separate from whether the Caps Lock key is held.

```
pf input state [flags]
```

### Examples

```
pf input state --key shift
pf input state --lock capslock
pf input state --button left
```

### Options

```
      --button string   mouse button to query (left, middle, right, button4, button5)
  -h, --help            help for state
      --key string      named key or modifier to query
      --lock string     lock-key state to query (capslock, numlock, scrolllock)
      --output string   output format: json (default "json")
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

