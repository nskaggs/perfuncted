## pf input select-file

Select a local file in an existing native file picker, preserving clipboard

```
pf input select-file [flags]
```

### Options

```
      --confirm[=true|false]   press final Enter to accept selection (default true)
  -h, --help[=true|false]      help for select-file
      --id string              stable window ID of open file chooser
      --path string            absolute path to existing file
      --title string           unique file chooser title
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

