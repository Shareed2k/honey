---
id: honey_completion_zsh
title: honey completion zsh
---

## honey completion zsh

Generate the autocompletion script for zsh

### Synopsis

Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" &gt;&gt; ~/.zshrc

To load completions in your current shell session:

	source &lt;(honey completion zsh)

To load completions for every new session, execute once:

#### Linux:

```bash
honey completion zsh > "${fpath[1]}/_honey"
```

#### macOS:

```bash
honey completion zsh > $(brew --prefix)/share/zsh/site-functions/_honey
```

You will need to start a new shell for this setup to take effect.


```
honey completion zsh [flags]
```

### Options

```
  -h, --help              help for zsh
      --no-descriptions   disable completion descriptions
```

### Options inherited from parent commands

```
      --cache-dir string     Override cache directory (default: XDG_CACHE_HOME/honey)
      --cache-ttl duration   Cache time-to-live (host discovery) (default 10m0s)
      --debug-log string     Path to write debug logs (disables debug logging if empty)
      --no-cache             Bypass read/write cache (host discovery)
      --record-dir string    Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
      --refresh              Ignore cached entries and refresh (host discovery)
```

### SEE ALSO

* [honey completion](honey_completion.md)	 - Generate the autocompletion script for the specified shell

