## pf completion zsh

Generate the autocompletion script for zsh

### Synopsis

Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

	source <(pf completion zsh)

To load completions for every new session, execute once:

#### Linux:

	pf completion zsh > "${fpath[1]}/_pf"

#### macOS:

	pf completion zsh > $(brew --prefix)/share/zsh/site-functions/_pf

You will need to start a new shell for this setup to take effect.


```
pf completion zsh [flags]
```

### Options

```
  -h, --help              help for zsh
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

