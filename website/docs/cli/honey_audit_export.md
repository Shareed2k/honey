---
id: honey_audit_export
title: honey audit export
---

## honey audit export

Print audit events as jsonl, table, or csv

```
honey audit export [flags]
```

### Options

```
      --action string     Filter by action
      --actor string      Filter by actor
      --decision string   Filter by decision
      --format string     Output format: jsonl, table, csv (default "jsonl")
  -h, --help              help for export
      --since string      Only show events after this duration ago (e.g. 1h, 30m)
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

* [honey audit](honey_audit.md)	 - Inspect the audit log

