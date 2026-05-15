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
      --debug-log string     Path to write debug logs (disables debug logging if empty)
  -h, --help                 help for honey
      --no-cache             Bypass read/write cache (host discovery)
      --record-dir string    Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
      --refresh              Ignore cached entries and refresh (host discovery)
  -v, --version              version for honey
```

### SEE ALSO

* [honey backends](honey_backends.md)	 - List backends defined in the honey config file
* [honey completion](honey_completion.md)	 - Generate the autocompletion script for the specified shell
* [honey config](honey_config.md)	 - Manage honey configuration
* [honey cue-exec](honey_cue-exec.md)	 - Resolve a CUE recipe against search results and optionally run steps over SSH
* [honey cue-validate](honey_cue-validate.md)	 - Validate a CUE remote recipe (commands and/or SFTP put/get steps)
* [honey inventory](honey_inventory.md)	 - Print Ansible-compatible JSON dynamic inventory from the same search as honey search
* [honey mcp](honey_mcp.md)	 - Run the Model Context Protocol (stdio) server
* [honey search](honey_search.md)	 - Search instances across providers in parallel
* [honey version](honey_version.md)	 - Print version, commit, date, and logo
* [honey web](honey_web.md)	 - Start embedded web UI (loopback + token) for backends, search, config, SSH terminal, and uploads

