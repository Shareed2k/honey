---
id: honey_doctor
title: honey doctor
---

## honey doctor

Check honey installation health: config, plugins, OPA policy, SSH key, and more

```
honey doctor [flags]
```

### Options

```
  -h, --help      help for doctor
      --mcp       Include MCP-specific checks
      --plugins   Include plugin checks
      --web       Include web server listen-addr check
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

