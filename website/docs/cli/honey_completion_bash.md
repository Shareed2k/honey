---
id: honey_completion_bash
title: honey completion bash
---

## honey completion bash

Generate the autocompletion script for bash

### Synopsis

Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

	source &lt;(honey completion bash)

To load completions for every new session, execute once:

#### Linux:

	honey completion bash &gt; /etc/bash_completion.d/honey

#### macOS:

	honey completion bash &gt; $(brew --prefix)/etc/bash_completion.d/honey

You will need to start a new shell for this setup to take effect.


```
honey completion bash
```

### Options

```
  -h, --help              help for bash
      --no-descriptions   disable completion descriptions
```

### Options inherited from parent commands

```
      --debug-log string   Path to write debug logs (disables debug logging if empty)
```

### SEE ALSO

* [honey completion](honey_completion.md)	 - Generate the autocompletion script for the specified shell

