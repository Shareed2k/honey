---
id: honey
title: honey
---

## honey

DevOps tool to help find an instance in sea of clouds

### Synopsis

Search and operate on instances across GCP, AWS, Kubernetes, Consul, and Proxmox.

### Options

```
      --cache-dir string     Override cache directory (default: XDG_CACHE_HOME/honey)
      --cache-ttl duration   Cache time-to-live (host discovery) (default 10m0s)
      --config string        Path to honey YAML (optional; also HONEY_CONFIG or default paths)
      --debug-log string     Path to write debug logs (disables debug logging if empty)
  -h, --help                 help for honey
      --no-cache             Bypass read/write cache (host discovery)
      --record-dir string    Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
      --refresh              Ignore cached entries and refresh (host discovery)
  -v, --version              version for honey
```

### SEE ALSO

* [honey alert](honey_alert.md)	 - Alert investigation tools
* [honey app](honey_app.md)	 - Manage and connect to application proxies
* [honey audit](honey_audit.md)	 - Inspect the audit log
* [honey backends](honey_backends.md)	 - List backends defined in the honey config file
* [honey completion](honey_completion.md)	 - Generate the autocompletion script for the specified shell
* [honey config](honey_config.md)	 - Manage honey configuration
* [honey cue-exec](honey_cue-exec.md)	 - Resolve a CUE recipe against search results and optionally run steps over SSH
* [honey cue-validate](honey_cue-validate.md)	 - Validate a CUE remote recipe (commands and/or SFTP put/get steps)
* [honey device](honey_device.md)	 - Manage device mTLS enrollment
* [honey doctor](honey_doctor.md)	 - Check honey installation health: config, plugins, OPA policy, SSH key, and more
* [honey egress](honey_egress.md)	 - Route traffic through a honey host via SOCKS5 (VPN-like exit)
* [honey exec](honey_exec.md)	 - Run a shell command on matching hosts in parallel
* [honey inventory](honey_inventory.md)	 - Print Ansible-compatible JSON dynamic inventory from the same search as honey search
* [honey logs](honey_logs.md)	 - Aggregate logs across matching hosts, pods, and containers
* [honey macros](honey_macros.md)	 - Run predefined macros from honeyfile manifest
* [honey mcp](honey_mcp.md)	 - Run the Model Context Protocol (stdio) server
* [honey plugins](honey_plugins.md)	 - Manage WASM plugins
* [honey proxy](honey_proxy.md)	 - Manage active proxy sessions
* [honey recordings](honey_recordings.md)	 - Manage session recordings
* [honey search](honey_search.md)	 - Search instances across providers in parallel
* [honey secrets](honey_secrets.md)	 - Encrypt and decrypt recipe secure:v1 secret refs
* [honey version](honey_version.md)	 - Print version, commit, date, and logo
* [honey web](honey_web.md)	 - Start embedded web UI (token-protected) for backends, search, config, SSH terminal, and uploads

