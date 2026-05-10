---
id: honey_inventory
title: honey inventory
---

## honey inventory

Print Ansible-compatible JSON dynamic inventory from the same search as honey search

### Synopsis

Runs the same parallel discovery as honey search (all search flags apply), then prints JSON
suitable for Ansible's script inventory plugin: with --list (or no --host), a top-level object
with a honey group, optional honey_provider_*, honey_region_*, honey_zone_* groups, and _meta.hostvars.

Each host gets ansible_host from the discovered PrimaryIP when present, ansible_user from --ssh-user
(or config defaults.ssh_user), plus honey_* variables and honey_meta_* keys from record meta.

For CI, AWX, or Ansible Tower: install the honey binary where the job runs, inject credentials the same
way as for honey search (GCP ADC, AWS_* / profiles, KUBECONFIG, CONSUL_*, Proxmox env or HONEY_CONFIG YAML),
then point inventory at this command, for example:

  ansible-playbook -i 'honey inventory --config /path/to/honey.yaml --provider gcp --' site.yml

Use a trailing -- before playbook args if needed. AWX custom inventory script: set the script to honey
with arguments inventory --list (and optional --config / --provider / --backends as needed).

```
honey inventory [name] [flags]
```

### Options

```
      --aws-profile string            AWS shared config profile
      --aws-region string             AWS region (default: from profile/env)
      --backends string               Comma-separated backend names (YAML backends.*.name); only those entries run
      --cache-dir string              Override cache directory (default: XDG_CACHE_HOME/honey)
      --cache-ttl duration            Cache time-to-live (default 1m0s)
      --config string                 Path to honey YAML (optional; also HONEY_CONFIG or default paths in README)
      --consul-addr string            Consul HTTP address (host:port, default CONSUL_HTTP_ADDR)
      --consul-datacenter string      Consul datacenter
      --consul-token string           Consul ACL token (or CONSUL_HTTP_TOKEN)
      --gcp-project string            GCP project (or GOOGLE_CLOUD_PROJECT / GCP_PROJECT)
      --gcp-zone string               Limit GCP to a single zone (default: all zones)
  -h, --help                          help for inventory
      --host string                   Ansible script inventory: emit JSON object of host variables for this inventory name; unknown hosts print {}
      --json                          Print results as JSON (same as --output=json)
      --k8s-debug-image string        Container image used for ephemeral debug containers (default: alpine:3.23)
      --k8s-mode string               Kubernetes search mode: nodes or pods (default "nodes")
      --kube-context string           Kubernetes context override
      --kubeconfig string             Path to kubeconfig file
      --list                          Ansible script inventory: emit full JSON (Ansible passes this; optional when not using --host)
      --name string                   Substring filter on instance/node/pod name (case-insensitive)
      --name-regex string             Regex filter on name (overrides --name substring)
      --no-cache                      Bypass read/write cache
      --no-ui                         Skip interactive UI (same as --output=json)
  -o, --output string                 Output format: tui, table, json (default "tui")
      --provider string               Comma-separated: gcp,aws,k8s,consul,proxmox (default: all)
      --proxmox-insecure              Skip TLS verification for Proxmox
      --proxmox-password string       Proxmox password
      --proxmox-token-id string       Proxmox token ID (e.g. root@pam!token)
      --proxmox-token-secret string   Proxmox token secret
      --proxmox-url string            Proxmox API URL (e.g. https://10.0.0.1:8006/api2/json)
      --proxmox-user string           Proxmox user (e.g. root@pam)
      --refresh                       Ignore cached entries and refresh
      --ssh-user string               Default SSH user for connect actions (default "shareed2k")
```

### Options inherited from parent commands

```
      --debug-log string    Path to write debug logs (disables debug logging if empty)
      --record-dir string   Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds

