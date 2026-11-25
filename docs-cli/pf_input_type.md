## pf input type

Type a string or send keys (e.g. {enter}, {ctrl+s})

### Synopsis

Interpret brace-delimited key sequences in text, including text read with --stdin. Use input type-literal for data that must not be interpreted as keys. Successful dispatch is not proof that the focused application accepted the input.

```
pf input type <text> [flags]
```

### Options

```
  -h, --help[=true|false]    help for type
      --stdin[=true|false]   read text from stdin
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

* [pf input](pf_input.md)	 - Mouse and keyboard injection

