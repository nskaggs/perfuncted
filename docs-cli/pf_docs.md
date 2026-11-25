## pf docs

Generate markdown documentation for the CLI

```
pf docs [flags]
```

### Options

```
  -d, --dir string          directory to write markdown files (default "./docs-cli")
  -h, --help[=true|false]   help for docs
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

