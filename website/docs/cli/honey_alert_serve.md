---
id: honey_alert_serve
title: honey alert serve
---

## honey alert serve

Start Alertmanager webhook receiver

### Synopsis

Start an HTTP server that receives Alertmanager webhook payloads,
deduplicates alerts, resolves matching hosts via alert_mappings, runs
investigation commands via SSH, and notifies configured channels.

Configure Alertmanager receiver:
  receivers:
    - name: honey
      webhook_configs:
        - url: http://honey-host:9095/webhook/alert
          http_config:
            bearer_token: "my-secret-token"

```
honey alert serve [flags]
```

### Options

```
  -h, --help           help for serve
      --port int       Override config alert_webhook.port (default 9095)
      --token string   Override config alert_webhook.token
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

* [honey alert](honey_alert.md)	 - Alert investigation tools

