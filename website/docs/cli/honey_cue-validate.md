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
      --debug-log string    Path to write debug logs (disables debug logging if empty)
      --record-dir string   Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds

