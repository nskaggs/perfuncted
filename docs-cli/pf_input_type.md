## pf input type

Type literal text without interpreting key syntax

### Synopsis

Type literal text without interpreting key syntax. Successful dispatch does not prove application acceptance.

```
pf input type <text> [flags]
```

### Options

```
  -h, --help[=true|false]    help for type
      --stdin[=true|false]   read text from stdin
      --window string        target an exact, unique window title
      --window-id string     target a unique stable window ID
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

