---
id: honey_exec
title: honey exec
---

## honey exec

Run a shell command on matching hosts in parallel

```
honey exec <target> <command> [flags]
```

### Examples

```
  honey exec "web-*" --parallel 50 --retry 3 --timeout 10s "systemctl restart nginx"
  honey --backends gcp-stg2 exec postgres /usr/bin/uptime
  honey exec "api-*" --provider k8s --run-as root "journalctl -u nginx -n 50"
```

### Options

```
  -o, --output string     Output format: text or json (default "text")
      --parallel int      Maximum concurrent command executions (default 20)
      --quiet             Show status lines only (no stdout blocks)
      --retry int         Retry attempts per host (1 disables retries) (default 1)
      --run-as string     Run command as this remote user via sudo -n
      --shell string      Command shell: auto, sh, bash, raw, powershell (default "auto")
      --timeout duration  Per-host command timeout (e.g. 10s, 2m); 0 disables
```

### Options inherited from parent commands

```
      --backends string      Comma-separated backend names (YAML backends.*.name); only those entries run
      --cache-dir string     Override cache directory (default: XDG_CACHE_HOME/honey)
      --cache-ttl duration   Cache time-to-live (host discovery) (default 10m0s)
      --config string        Path to honey YAML (optional; also HONEY_CONFIG or default paths in README)
      --debug-log string     Path to write debug logs (disables debug logging if empty)
      --no-cache             Bypass read/write cache (host discovery)
      --provider string      Comma-separated provider IDs to restrict search
      --record-dir string    Session recording directory; overrides defaults.record_dir
      --refresh              Ignore cached entries and refresh (host discovery)
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds
