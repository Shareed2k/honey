---
id: honey_plugins_install
title: honey plugins install
---

## honey plugins install

Install a plugin from a URL, archive, or local directory

### Synopsis

Install a WASM plugin into the configured plugins directory.

&lt;src&gt; may be:
  - An https:// URL to a .tar.gz or .zip archive
  - A local .tar.gz or .zip file
  - A local directory containing plugin.yaml and plugin.wasm

The plugin is installed to &lt;plugins-dir&gt;/&lt;plugin-id&gt;/.


```
honey plugins install &lt;src&gt; [flags]
```

### Options

```
      --dir string   Override plugins directory (default: from config or ~/.config/honey/plugins)
  -f, --force        Overwrite existing plugin
  -h, --help         help for install
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

* [honey plugins](honey_plugins.md)	 - Manage WASM plugins

