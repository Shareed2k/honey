---
id: honey_recordings_export
title: honey recordings export
---

## honey recordings export

Export a recording to asciinema v3 .cast format

```
honey recordings export &lt;basename&gt; [flags]
```

### Options

```
  -h, --help            help for export
  -o, --output string   Write to file instead of stdout
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

