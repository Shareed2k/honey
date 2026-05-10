---
id: honey_completion_fish
title: honey completion fish
---

## honey completion fish

Generate the autocompletion script for fish

### Synopsis

Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	honey completion fish | source

To load completions for every new session, execute once:

	honey completion fish &gt; ~/.config/fish/completions/honey.fish

You will need to start a new shell for this setup to take effect.


```
honey completion fish [flags]
```

### Options

```
  -h, --help              help for fish
      --no-descriptions   disable completion descriptions
```

### Options inherited from parent commands

```
      --debug-log string    Path to write debug logs (disables debug logging if empty)
      --record-dir string   Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
```

### SEE ALSO

* [honey completion](honey_completion.md)	 - Generate the autocompletion script for the specified shell

