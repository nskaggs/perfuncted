## pf completion fish

Generate the autocompletion script for fish

### Synopsis

Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	pf completion fish | source

To load completions for every new session, execute once:

	pf completion fish > ~/.config/fish/completions/pf.fish

You will need to start a new shell for this setup to take effect.


```
pf completion fish [flags]
```

### Options

```
  -h, --help              help for fish
      --no-descriptions   disable completion descriptions
```

### Options inherited from parent commands

```
      --max-x int32            input coordinate space width (default 1920)
      --max-y int32            input coordinate space height (default 1080)
      --nested                 auto-detect and connect to a nested Wayland session in /tmp
      --trace-actions          print each API action to stderr as it runs
      --trace-delay duration   sleep after each traced action
```

### SEE ALSO

* [pf completion](pf_completion.md)	 - Generate the autocompletion script for the specified shell

