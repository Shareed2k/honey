---
id: honey_completion_powershell
title: honey completion powershell
---

## honey completion powershell

Generate the autocompletion script for powershell

### Synopsis

Generate the autocompletion script for powershell.

To load completions in your current shell session:

	honey completion powershell | Out-String | Invoke-Expression

To load completions for every new session, add the output of the above command
to your powershell profile.


```
honey completion powershell [flags]
```

### Options

```
  -h, --help              help for powershell
      --no-descriptions   disable completion descriptions
```

### Options inherited from parent commands

```
      --debug-log string   Path to write debug logs (disables debug logging if empty)
```

### SEE ALSO

* [honey completion](honey_completion.md)	 - Generate the autocompletion script for the specified shell

