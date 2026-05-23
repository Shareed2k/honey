---
id: honey_web
title: honey web
---

## honey web

Start embedded web UI (loopback + token) for backends, search, config, SSH terminal, and uploads

```
honey web [flags]
```

### Options

```
      --agent-bin string               Explicit path to honey-transfer-agent binary (optional)
      --agent-build-cache-dir string   Directory used to cache auto-built honey-transfer-agent binary
      --config string                  Path to honey YAML (optional; same as honey search)
      --files-root string              Local filesystem root for the web file browser (default: $HONEY_FILES_ROOT or $HOME)
  -h, --help                           help for web
      --listen string                  Listen address (host:port); must be loopback for safe default (default "127.0.0.1:8765")
      --metrics-listen string          Optional loopback host:port for Prometheus /metrics (e.g. 127.0.0.1:9091)
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

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds

