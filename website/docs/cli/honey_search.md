---
id: honey_search
title: honey search
---

## honey search

Search instances across providers in parallel

```
honey search [name] [flags]
```

Host discovery cache (`--cache-ttl`, `--cache-dir`, `--no-cache`, `--refresh`) is configured with **global** flags on the `honey` root command (see **Global Flags** in `honey search --help` and `honey --help`). You can place them before or after the subcommand (for example `honey --refresh search foo` or `honey search --refresh foo`). Default cache TTL is **10 minutes** unless overridden by `defaults.cache_ttl` in honey YAML.

### Options

```
      --aws-profile string            AWS shared config profile
      --aws-region string             AWS region (default: from profile/env)
      --backends string               Comma-separated backend names (YAML backends.*.name); only those entries run
      --config string                 Path to honey YAML (optional; also HONEY_CONFIG or default paths in README)
      --consul-addr string            Consul HTTP address (host:port, default CONSUL_HTTP_ADDR)
      --consul-datacenter string      Consul datacenter
      --consul-token string           Consul ACL token (or CONSUL_HTTP_TOKEN)
      --gcp-project string            GCP project (or GOOGLE_CLOUD_PROJECT / GCP_PROJECT)
      --gcp-zone string               Limit GCP to a single zone (default: all zones)
  -h, --help                          help for search
      --json                          Print results as JSON (same as --output=json)
      --k8s-debug-image string        Container image used for ephemeral debug containers (default: alpine:3.23)
      --k8s-mode string               Kubernetes search mode: nodes or pods (default "nodes")
      --kube-context string           Kubernetes context override
      --kubeconfig string             Path to kubeconfig file
      --name string                   Substring filter on instance/node/pod name (case-insensitive)
      --name-regex string             Regex filter on name (overrides --name substring)
      --no-ui                         Skip interactive UI (same as --output=json)
  -o, --output string                 Output format: tui, table, json (default "tui")
      --provider string               Comma-separated: gcp,aws,k8s,consul,proxmox (default: all)
      --proxmox-insecure              Skip TLS verification for Proxmox
      --proxmox-password string       Proxmox password
      --proxmox-token-id string       Proxmox token ID (e.g. root@pam!token)
      --proxmox-token-secret string   Proxmox token secret
      --proxmox-url string            Proxmox API URL (e.g. https://10.0.0.1:8006/api2/json)
      --proxmox-user string           Proxmox user (e.g. root@pam)
      --ssh-user string               Default SSH user for connect actions (default "shareed2k")
```

### Global flags

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

