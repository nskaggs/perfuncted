## pf completion bash

Generate the autocompletion script for bash

### Synopsis

Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

	source <(pf completion bash)

To load completions for every new session, execute once:

#### Linux:

	pf completion bash > /etc/bash_completion.d/pf

#### macOS:

	pf completion bash > $(brew --prefix)/etc/bash_completion.d/pf

You will need to start a new shell for this setup to take effect.


```
pf completion bash
```

### Options

```
  -h, --help              help for bash
      --no-descriptions   disable completion descriptions
```

### Options inherited from parent commands

```
      --max-x int32   input coordinate space width (default 1920)
      --max-y int32   input coordinate space height (default 1080)
      --nested        auto-detect and connect to a nested Wayland session in /tmp
```

### SEE ALSO

* [pf completion](pf_completion.md)	 - Generate the autocompletion script for the specified shell

