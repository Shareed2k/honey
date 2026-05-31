---
id: honey_exec
title: honey exec
---

## honey exec

Run a shell command on matching hosts in parallel

### Synopsis

Runs a shell command on all connectable records returned by search.

Supports SSH hosts, Kubernetes pods, Docker containers/tasks, and TrueNAS records
through the same executor pipeline used by the TUI and recipe execution.

```
honey exec &lt;target&gt; &lt;command&gt; [flags]
```

### Examples

```
  honey exec "web-*" --parallel 50 --retry 3 --timeout 10s "systemctl restart nginx"
  honey --backends gcp-stg2 exec postgres /usr/bin/uptime
  honey exec "api-*" --provider k8s --run-as root "journalctl -u nginx -n 50"
```

### Options

```
      --aws-profile string            AWS shared config profile
      --aws-region string             AWS region (default: from profile/env)
      --backends string               Comma-separated backend names (YAML backends.*.name); only those entries run
      --consul-addr string            Consul HTTP address (host:port, default CONSUL_HTTP_ADDR)
      --consul-datacenter string      Consul datacenter
      --consul-token string           Consul ACL token (or CONSUL_HTTP_TOKEN)
      --docker-all                    Include stopped containers in docker search
      --docker-host string            Docker host (unix://, tcp://, ssh://; default: DOCKER_HOST / local socket)
      --docker-mode string            Docker search mode: containers, swarm, or both (default "containers")
      --docker-platform string        Remote Docker host OS: linux or windows (default "linux")
      --docker-socket string          Remote Docker socket (default /var/run/docker.sock on linux)
      --docker-via-local string       Docker via Honey SSH: backends.local name
      --docker-via-ssh-host string    Docker via Honey SSH: explicit host
      --gcp-project string            GCP project (or GOOGLE_CLOUD_PROJECT / GCP_PROJECT)
      --gcp-zone string               Limit GCP to a single zone (default: all zones)
  -h, --help                          help for exec
      --k8s-debug-image string        Container image used for ephemeral debug containers (default: alpine:3.23)
      --k8s-mode string               Kubernetes search mode: nodes or pods (default "pods")
      --kube-context string           Kubernetes context override
      --kubeconfig string             Path to kubeconfig file
      --name string                   Substring filter on instance/node/pod name (case-insensitive)
      --name-regex string             Regex filter on name (overrides --name substring)
  -o, --output string                 Output format: text or json (default "text")
      --parallel int                  Maximum concurrent command executions (default 20)
      --provider string               Comma-separated: gcp,aws,k8s,consul,proxmox,truenas,docker,local (default: all)
      --proxmox-insecure              Skip TLS verification for Proxmox
      --proxmox-password string       Proxmox password
      --proxmox-token-id string       Proxmox token ID (e.g. root@pam!token)
      --proxmox-token-secret string   Proxmox token secret
      --proxmox-url string            Proxmox API URL (e.g. https://10.0.0.1:8006/api2/json)
      --proxmox-user string           Proxmox user (e.g. root@pam)
      --quiet                         Show status lines only (no stdout blocks)
      --retry int                     Retry attempts per host (1 disables retries) (default 1)
      --run-as string                 Run command as this remote user via sudo -n
      --shell string                  Command shell: auto, sh, bash, raw, powershell (default "auto")
      --ssh-user string               Default SSH user for connect actions (defaults to config or OS user)
      --timeout duration              Per-host command timeout (e.g. 10s, 2m); 0 disables
      --truenas-api-key string        TrueNAS API key (or TRUENAS_API_KEY)
      --truenas-insecure              Skip TLS verification for TrueNAS
      --truenas-url string            TrueNAS SCALE URL (https://host or wss://host/api/current)
      --truenas-user string           TrueNAS API key username (default root)
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

