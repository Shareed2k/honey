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
```

### Options inherited from parent commands

```
      --debug-log string    Path to write debug logs (disables debug logging if empty)
      --record-dir string   Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds

