---
id: honey_cue-exec
title: honey cue-exec
---

## honey cue-exec

Resolve a CUE recipe against search results and optionally run steps over SSH

### Synopsis

Loads a .cue recipe (see examples/recipe), runs the same host search as honey search
(share all search flags), resolves each step's host field using search results:
literal IP, exact name match, host "*" for all rows with an IP, or host "re:PATTERN"
for a Go regexp (RE2) matched against each row's name (only rows with PrimaryIP).

Each step is exactly one of: shell command, put (upload), get (download),
script (upload a local file then run it with sh on the same SSH connection),
agent_transfer (A→cloud→B using the transfer agent; requires --config when using cloud_backend_ref),
or ai (terminal local summarizer after prior steps; host must be "_"; OPENAI_API_KEY when executing).
Relative local paths are resolved against the recipe file's directory.

Then either prints a plan (--execute=false, default) or runs each step (--execute).

Optional positional name is forwarded like search: one extra argument sets the
name substring filter when --name / --name-regex are not set.

Use recipe.defaults.run_as or per-step run_as for command and script steps
(sudo -n on the remote run only); put/get SFTP uses the SSH login user.

Optional recipe.defaults.env and per-step env (map of NAME to value) set
export assignments before the shell command or sh &lt;script&gt; on the remote;
step keys override defaults. Optional defaults.secrets and step secrets (command/script
only) map NAME to ref strings (env:VAR, keyring://…, etc.) resolved at execute time; dry-run
shows redacted placeholders, not resolved values. Not allowed on put/get/ai steps.

Repeat -e/--env KEY=value to set remote variables from the CLI; they override
recipe env on duplicate keys (command and script steps only).

With global --record-dir or defaults.record_dir in config, writes one batch .hrec.jsonl per
invocation when recording is enabled: explicit --record-dir, or record_dir set in honey YAML
(built-in default records/ alone does not enable cue-exec batch logs). Dry-run records the plan text; --execute records each step result.

```
honey cue-exec &lt;recipe.cue&gt; [name] [flags]
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
  -e, --env stringArray               Remote env for command/script (repeat: -e KEY=value); overrides recipe env on duplicate keys
      --execute                       Run steps over SSH/SFTP (default: dry-run, print resolved plan only)
      --gcp-project string            GCP project (or GOOGLE_CLOUD_PROJECT / GCP_PROJECT)
      --gcp-zone string               Limit GCP to a single zone (default: all zones)
  -h, --help                          help for cue-exec
      --json                          Print results as JSON (same as --output=json)
      --k8s-debug-image string        Container image used for ephemeral debug containers (default: alpine:3.23)
      --k8s-mode string               Kubernetes search mode: nodes or pods (default "nodes")
      --kube-context string           Kubernetes context override
      --kubeconfig string             Path to kubeconfig file
      --name string                   Substring filter on instance/node/pod name (case-insensitive)
      --name-regex string             Regex filter on name (overrides --name substring)
      --no-ui                         Skip interactive UI (same as --output=json)
  -o, --output string                 Output format: tui, table, json (default "tui")
      --provider string               Comma-separated: gcp,aws,k8s,consul,proxmox,truenas,docker,local (default: all)
      --proxmox-insecure              Skip TLS verification for Proxmox
      --proxmox-password string       Proxmox password
      --proxmox-token-id string       Proxmox token ID (e.g. root@pam!token)
      --proxmox-token-secret string   Proxmox token secret
      --proxmox-url string            Proxmox API URL (e.g. https://10.0.0.1:8006/api2/json)
      --proxmox-user string           Proxmox user (e.g. root@pam)
      --retry-failed string           Re-run only hosts that did not succeed in this recording (basename, e.g. 20260529_….hrec.jsonl)
      --ssh-user string               Default SSH user for connect actions (defaults to config or OS user)
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

