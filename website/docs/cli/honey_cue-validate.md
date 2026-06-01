---
id: honey_cue-validate
title: honey cue-validate
---

## honey cue-validate

Validate a CUE remote recipe (commands and/or SFTP put/get steps)

### Synopsis

Parses a .cue file and checks that the top-level "recipe" field matches
the built-in schema: name (string) and steps (each step has host and exactly one
of command, put, get, or script \{local, remote\}; optional run_as on command/script steps).

```
honey cue-validate &lt;file.cue&gt; [flags]
```

### Options

```
  -h, --help   help for cue-validate
```

### Options inherited from parent commands

```
      --cache-dir string     Override cache directory (default: XDG_CACHE_HOME/honey)
      --cache-ttl duration   Cache time-to-live (host discovery) (default 10m0s)
      --config string        Path to honey YAML (optional; also HONEY_CONFIG or default paths)
      --debug-log string     Path to write debug logs (disables debug logging if empty)
      --no-cache             Bypass read/write cache (host discovery)
      --record-dir string    Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
      --refresh              Ignore cached entries and refresh (host discovery)
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds

