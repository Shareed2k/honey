---
id: honey_plugins
title: honey plugins
---

## honey plugins

Manage WASM plugins

### Options

```
  -h, --help   help for plugins
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
* [honey plugins inspect](honey_plugins_inspect.md)	 - Show manifest, capabilities, and effective network policy for a plugin
* [honey plugins install](honey_plugins_install.md)	 - Install a plugin from a URL, archive, or local directory
* [honey plugins list](honey_plugins_list.md)	 - Show plugin id, capabilities, and path

