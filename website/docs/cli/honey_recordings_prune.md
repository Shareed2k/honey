---
id: honey_recordings_prune
title: honey recordings prune
---

## honey recordings prune

Delete recordings older than a given age

```
honey recordings prune [flags]
```

### Examples

```
  honey recordings prune --older-than 7d
  honey recordings prune --older-than 720h --dry-run
```

### Options

```
      --dry-run             List recordings that would be deleted without deleting them
  -h, --help                help for prune
      --older-than string   Delete recordings older than this age (e.g. 7d, 720h)
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

* [honey recordings](honey_recordings.md)	 - Manage session recordings

