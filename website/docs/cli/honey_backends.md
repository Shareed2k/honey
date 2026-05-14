---
id: honey_backends
title: honey backends
---

## honey backends

List backends defined in the honey config file

### Synopsis

Resolves the config file the same way as search (--config, HONEY_CONFIG, or default paths),
	and lists all backends with a "name" property across all providers.

```
honey backends [flags]
```

### Options

```
      --config string   Path to honey YAML (optional; also HONEY_CONFIG or default paths)
  -h, --help            help for backends
      --json            Print JSON (config_path + backends) instead of a table
```

### Options inherited from parent commands

```
      --debug-log string    Path to write debug logs (disables debug logging if empty)
      --record-dir string   Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds

