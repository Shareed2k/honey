# honey

CLI to search **GCP Compute Engine**, **AWS EC2**, **Kubernetes** (nodes or pods), **Docker Engine** (containers and Swarm tasks), **Consul** catalog nodes, and **Proxmox VE** instances **in parallel**, optionally cache results, then use a **terminal UI** to SSH, **`docker exec`** into containers, or open an **SSH local forward** (`-L`) via the system `ssh` binary.

## Prerequisites

- Go 1.26.2+ (see `go` directive in `go.mod`; use this toolchain or newer so `govulncheck` reports clean stdlib fixes from Go 1.26.1/1.26.2)
- Credentials for each backend you enable (see below)

After cloning, generate checksums:

```bash
cd honey
go mod tidy
```

## Install

**Homebrew (macOS)**:
Because this tool relies on Homebrew Casks, it is installed via the `--cask` flag:
```bash
brew install --cask shareed2k/tap/honey
```

## Build

```bash
go build -o honey ./cmd/honey
```

## MCP server (stdio)

`honey mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io/) server over **stdin/stdout** using the official [`go-sdk`](https://github.com/modelcontextprotocol/go-sdk). **Do not log to stdout** (only stderr); stdout is reserved for the JSON-RPC stream.

**Tools**

| Tool | Purpose |
|------|---------|
| `search_hosts` | Same parallel search as `honey search`; arguments mirror flags (snake_case JSON). Optional `config_path`; otherwise uses `HONEY_CONFIG` / default paths. |
| `list_backends` | Returns configured backends from YAML (`kind`, `name`, `hint`). Requires a resolvable config file. |

**Cursor** (example `mcp.json` fragment):

```json
{
  "mcpServers": {
    "honey": {
      "command": "/absolute/path/to/honey",
      "args": ["mcp"],
      "env": {
        "HONEY_CONFIG": "/absolute/path/to/honey.yaml"
      }
    }
  }
}
```

**LM Studio** ([MCP docs](https://lmstudio.ai/docs/app/mcp); app **0.3.17+**)

LM Studio uses the same `mcpServers` shape as Cursor. In the app: open the **Program** tab (right sidebar) → **Install** → **Edit `mcp.json`**, then merge a `honey` entry into the top-level `mcpServers` object (or create the file if it is empty).

Typical file locations:

- **macOS / Linux:** `~/.lmstudio/mcp.json`
- **Windows:** `%USERPROFILE%\.lmstudio\mcp.json`

Example (replace paths with your real `honey` binary and YAML config):

```json
{
  "mcpServers": {
    "honey": {
      "command": "/Users/you/bin/honey",
      "args": ["mcp"],
      "env": {
        "HONEY_CONFIG": "/Users/you/.config/honey/config.yaml"
      }
    }
  }
}
```

If you already have other servers under `mcpServers`, add only the `"honey": { ... }` block inside that object—do not duplicate the outer `"mcpServers"` key. After saving, restart the chat or reload tools if LM Studio does not pick up the server immediately. Enable or allow the **honey** tools under **App settings → Tools & integrations** (wording may vary by version) if the UI asks for permission.

**OpenCode** ([MCP servers](https://opencode.ai/docs/mcp-servers/), [config](https://opencode.ai/docs/config/))

OpenCode uses a top-level **`mcp`** object (not `mcpServers`). Local stdio servers use **`type": "local"`** and **`command`** as an **array** of executable + args. Environment variables go under **`environment`** (not `env`).

Merge this into `~/.config/opencode/opencode.json` (global) or a project **`opencode.json` / `opencode.jsonc`**—see OpenCode’s config precedence docs.

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "honey": {
      "type": "local",
      "command": ["/absolute/path/to/honey", "mcp"],
      "enabled": true,
      "environment": {
        "HONEY_CONFIG": "/absolute/path/to/honey.yaml"
      }
    }
  }
}
```

Tools from this server appear with the **`honey_`** prefix (e.g. `honey_search_hosts`). You can mention `use honey` or a specific tool name in prompts; see OpenCode’s MCP docs for disabling servers or scoping tools per agent.

## Config file (optional)

You can define **multiple backends** per provider (for example two GCP projects or two Consul clusters) and optional **defaults**. If the file omits `backends` or leaves every backend list empty, behavior matches the flag-only mode (one implicit backend per provider).

Each list entry may set **`name`** (any stable string). Use **`--backends`** with a comma-separated list of those names (case-insensitive) to run only those entries—for example only `gcp-prod-us2` and `k8s-stg2`. Unnamed backends are skipped when `--backends` is set. Combine with **`--provider`** to further narrow by type (`gcp`, `k8s`, …).

**Lookup order** (first match wins):

1. `--config /path/to/file.yaml`
2. `HONEY_CONFIG`
3. `$XDG_CONFIG_HOME/honey/config.yaml`
4. `~/.config/honey/config.yaml` when `XDG_CONFIG_HOME` is unset
5. `~/.honey.yaml`

**Precedence:** CLI flags override config `defaults` when you pass the flag (Cobra “changed” semantics). Query flags (`--gcp-project`, `--consul-addr`, etc.) override per-backend YAML values at search time.

Example `~/.config/honey/config.yaml`:

```yaml
version: 1
defaults:
  cache_ttl: 5m
  ssh_user: deploy
  k8s_mode: nodes
  k8s_debug_image: "nicolaka/netshoot:latest"
backends:
  gcp:
    - name: gcp-team-a
      project: team-a-prod
    - name: gcp-team-b-zone
      project: team-b-prod
      zone: us-central1-a
  aws:
    - name: aws-prod-use1
      profile: production
      region: us-east-1
  kubernetes:
    - name: k8s-staging
      context: staging
      kubeconfig: ~/.kube/config.staging
      debug_image: "ubuntu:latest"
  consul:
    - name: consul-prod
      addr: "10.0.0.5:8500"
      token: "secret"
  proxmox:
    - name: "pve-cluster"
      url: "https://10.0.0.10:8006/api2/json"
      user: "root@pam"
      password: "my-password"
      insecure: true
    - name: "pve-token"
      url: "https://10.0.0.11:8006/api2/json"
      token_id: "root@pam!mytoken"
      token_secret: "1234abcd-1234-abcd-1234-abcd1234abcd"
  truenas:
    - name: scale-lab
      url: "https://truenas.example.com"
      username: root
      api_key: "1-REPLACE_ME"
      insecure: false
      include_appliance: true
      include_vms: true
      include_virt: true
      ssh_user: root
  docker:
    - name: local
      host: ""              # default: DOCKER_HOST / local socket
      mode: containers      # containers | swarm | both
    - name: vm-docker
      via_local: lab          # backends.local[].name (or host name, or lab/vm1)
      socket: /var/run/docker.sock
      run_as: root            # optional: SSH as defaults.ssh_user, Docker API via sudo + docker system dial-stdio
      platform: linux       # linux | windows (daemon host OS)
      mode: containers
    - name: builder
      via_ssh:
        host: 10.0.0.1
        port: 2222
        user: lab
        identity_file: ~/.ssh/id_ed25519
      socket: /var/run/docker.sock
    - name: moby-ssh
      host: ssh://ops@other-host   # Moby built-in SSH (not Honey SSH)
      mode: both
```

### Docker provider

`honey search --provider docker` lists **containers** and/or **Swarm tasks** via the Docker Engine API (not SSH into container networks). Interactive sessions use **`docker exec`**; the TUI/web file browser uses **`docker cp`** semantics (`CopyTo`/`CopyFrom` API) plus exec helpers for directory listing.

| Connection | Config / flags | SSH stack | Notes |
|------------|----------------|-----------|--------|
| Local / `DOCKER_HOST` | `host: ""` or `unix://` / `tcp://` | — | Default socket |
| Moby `ssh://` | `host: ssh://user@host` | Docker SDK only | No ProxyJump / `~/.ssh/config` integration |
| **Honey SSH** | `via_local` or `via_ssh` + `socket` | Honey `sshclient` | Dials remote Engine socket over SSH; reuses TUI SSH session when already connected |
| **Auto-discover** | `HONEY_FEATURE_DOCKER_VIA_PROVIDERS=1` + `--docker-discover-providers gcp,aws` | Honey SSH | Second search pass; not configurable in YAML |

```bash
# Local Docker Desktop / Colima (default socket or DOCKER_HOST)
./honey search --provider docker

# Remote daemon over Moby ssh:// (Docker SDK SSH)
./honey search --provider docker --docker-host ssh://ops@docker-host.internal

# Honey SSH to a VM's docker.sock (CLI overrides for default backend)
./honey search --provider docker --docker-via-local ssh-target --docker-socket /var/run/docker.sock

# Windows daemon host (often needs explicit TCP socket)
./honey search --provider docker --docker-via-ssh-host winvm --docker-platform windows --docker-socket tcp://127.0.0.1:2375

# Auto-discover containers on GCP/AWS VMs (feature flag required)
export HONEY_FEATURE_DOCKER_VIA_PROVIDERS=1
./honey search --provider gcp,aws,docker --docker-discover-providers gcp,aws

# GCP/AWS: SSH as ubuntu, docker.sock only for root (passwordless sudo required)
./honey search --provider gcp,aws,docker \
  --docker-discover-providers gcp,aws \
  --ssh-user ubuntu \
  --docker-discover-run-as root

# Swarm tasks only
./honey search --provider docker --docker-mode swarm

# Include stopped containers
./honey search --provider docker --docker-all
```

**TUI tip:** Connect SSH to the VM in the table first (`c`); Honey reuses that session for `via_local` Docker backends instead of opening a second SSH connection.

**Linux VMs:** either add the SSH user to the `docker` group, or set `run_as: root` on honey-ssh docker backends / `--docker-discover-run-as root` for auto-discover (uses `sudo -n` + `docker system dial-stdio`, Engine 23+, same idea as recipe [`run_as`](https://github.com/shareed2k/honey/blob/main/examples/recipe/with_run_as.cue)).

**Interactive terminal (TUI / web):** On a selected docker row with `meta.container_id`, **Enter** opens a TTY shell via **`docker exec`** (`sh` on Linux, `powershell.exe` on Windows containers)—not SSH into the container network. The web UI uses the same exec attach over **`GET /ws/ssh`** (see [Web UI](#web-ui-honey-web)). File browser and **Run command** use Engine API copy/exec.

**Parallel `e` and `*` marks:** Scope includes every **executable** row: VMs/pods with an IP, k8s pods, and docker containers with `container_id` (no `PrimaryIP` required). **Ctrl+a** marks all executable rows. Commands run through the same executor as **Enter** (`docker exec` for containers).

**Auto-discover and `--backends`:** Discover runs as a **second pass** only on VM records already returned by the first search (respecting `--provider`, `--backends`, and name filters). Example:

```bash
export HONEY_FEATURE_DOCKER_VIA_PROVIDERS=1
./honey search --backends gcp-stg2 --docker-discover-providers gcp \
  --ssh-user ubuntu --docker-discover-run-as root my-app
```

**Discovered container metadata** (JSON `meta`):

| Key | Meaning |
|-----|---------|
| `container_id` | Engine container ID (required for exec / terminal) |
| `docker_host` | API endpoint (`honey-ssh://…` or `unix://` / `tcp://`) |
| `docker_transport` | `honey_ssh` when dialed via VM SSH |
| `docker_vm` | Source VM name from the cloud search |
| `docker_vm_ip` | Internal IP used for Honey SSH dial to the daemon |
| `docker_vm_external_ip` | Public IP when the VM has one |
| `via_provider` | Cloud provider that owned the VM (`gcp`, `aws`, …) |
| `docker_discover` | `"1"` when the row came from auto-discover |

The table **IP** column may show the VM’s **external** address while Honey SSH still dials **`docker_vm_ip`** (internal). `extra_ips` can hold the internal address when both exist.

**Limitations:** **`t`** (SSH `-L` tunnels) is not supported on pure docker container rows. **`cue-exec`** / TUI **r** still resolve step `host` by **name**, literal IP, `*`, or `re:`—match the container **name** (or a VM IP for SSH-backed steps); there is no separate docker-specific host syntax beyond normal executor routing when the row matches.

## Usage

```bash
# Interactive table (default); optional positional name is a substring filter
./honey search my-host

# JSON output, no TUI
./honey search --json my-host

# Explicit config path (otherwise see "Config file" below)
./honey search --config ~/.config/honey/config.yaml my-host

# Limit providers
./honey search --provider aws,k8s web

# Only specific named backends from config (see backends.*.name)
./honey search --backends gcp-team-a,k8s-staging web
./honey search --backends gcp-prod-us2 --provider gcp my-node

# List backends from config (same config resolution as search)
./honey backends
./honey backends --json

# Regex filter
./honey search --name-regex '^prod-'

# Cache (default TTL 10m; flags are global — see `honey --help` Global Flags); force refresh
./honey search --refresh foo
./honey --refresh search foo
./honey search --no-cache foo
./honey search --cache-ttl 5m foo
```

### Ansible inventory (`honey inventory`)

`honey inventory` runs the **same discovery** as `honey search` (all search flags: `--config`, `--provider`, `--backends`, name filters, cache flags, per-provider auth, and so on) and prints **Ansible script-style JSON**: a `honey` group, optional `honey_provider_*`, `honey_region_*`, `honey_zone_*` groups, and `_meta.hostvars`. Each host gets `ansible_host` from the discovered primary IP when present, `ansible_user` from `--ssh-user` (or `defaults.ssh_user` in YAML), **`ansible_port`** when the row’s `meta.ssh_port` is a valid TCP port (1–65535; string or number in YAML/JSON), plus `honey_*` fields and `honey_meta_<key>` from each record’s `meta` map. **`meta.ssh_port` is not duplicated** as `honey_meta_ssh_port`—it is promoted to **`ansible_port`** only.

```bash
# Full inventory JSON (Ansible’s script plugin calls this with --list)
./honey inventory --list --config ~/.config/honey/config.yaml

# Narrow providers or backends like search
./honey inventory --list --provider gcp,aws --backends gcp-team-a

# Optional name substring (same as search positional)
./honey inventory --list web

# Vars for one inventory hostname (script --host); unknown host returns {}
./honey inventory --host web-1
```

**Local Ansible:** Ansible’s **script** inventory expects an **executable file** on disk. The `-i` argument must be **that file’s path**—Ansible does **not** parse a command line. This fails because the whole string is treated as one path:

```bash
# Wrong: no file literally named "/tmp/honey inventory --provider gcp --"
ansible-playbook -i '/tmp/honey inventory --provider gcp --' play.yml
```

Use a tiny wrapper that `exec`s honey and **forwards** `"$@"` (Ansible passes `--list` or `--host <name>`), then pass **only the wrapper path** to `-i`:

```bash
printf '%s\n' '#!/bin/sh' 'exec /path/to/honey inventory "$@"' > honey-ansible-inv && chmod +x honey-ansible-inv
ansible-playbook -i ./honey-ansible-inv site.yml
```

GCP-only example (wrapper bakes in `--provider gcp`; Ansible still appends `--list` / `--host` after your fixed args—order matters: put `"$@"` last):

```bash
printf '%s\n' '#!/bin/sh' 'exec /tmp/honey inventory --provider gcp "$@"' > /tmp/honey-inv-gcp && chmod +x /tmp/honey-inv-gcp
ansible-playbook -i /tmp/honey-inv-gcp ansible/playbooks/restart_es_playbook.yml
```

Copy from [`examples/ansible/honey_inventory_gcp.example.sh`](https://github.com/shareed2k/honey/blob/main/examples/ansible/honey_inventory_gcp.example.sh) and adjust the `honey` path.

Add other fixed flags inside the wrapper if you want (for example `exec /path/to/honey inventory --config "$HOME/.config/honey/config.yaml" --provider gcp "$@"`).

**Without a shell wrapper (inventory plugin):** use the YAML-driven inventory plugin shipped in this repo ([`contrib/ansible/inventory_plugins/honey.py`](https://github.com/shareed2k/honey/blob/main/contrib/ansible/inventory_plugins/honey.py)). Install it on Ansible’s inventory plugin path, then pass `-i` a small YAML file that sets `plugin: honey` and options such as `honey_binary`, `provider`, and `config`. Example config: [`contrib/ansible/honey.gcp.example.yml`](https://github.com/shareed2k/honey/blob/main/contrib/ansible/honey.gcp.example.yml). Quick run from a clone:

```bash
export ANSIBLE_INVENTORY_PLUGINS="$PWD/contrib/ansible/inventory_plugins"
ansible-playbook -i contrib/ansible/honey.gcp.example.yml ansible/playbooks/restart_es_playbook.yml -e cluster_name=ddd
```

Adjust `ANSIBLE_INVENTORY_PLUGINS` to your checkout path, and edit the YAML (or a copy) so `provider` / `config` match your environment. More detail: [`examples/ansible/README.md`](https://github.com/shareed2k/honey/blob/main/examples/ansible/README.md).

**CI / AWX / Ansible Tower:** install the `honey` binary on the execution environment and supply credentials the same way you would for `honey search` (for example `HONEY_CONFIG` pointing at a mounted secret, `GOOGLE_APPLICATION_CREDENTIALS`, `AWS_PROFILE` / instance role, `KUBECONFIG`, `CONSUL_HTTP_TOKEN`, or Proxmox flags baked into the wrapper or injected via environment). Use either the **inventory plugin** YAML (set `ANSIBLE_INVENTORY_PLUGINS` or install `honey.py` into the execution image’s plugin path) or a **custom inventory script** wrapper that runs `honey inventory` with `--list` / `--host`. Honey does **not** replace Tower or AWX; it only **feeds inventory JSON** from live APIs.

### CUE recipes (experimental)

Validate a playbook-shaped [CUE](https://cuelang.org/) file: each step has `host` and **exactly one** of `command`, `put` (SFTP upload), `get` (SFTP download), `script` (upload `local` → `remote`, then run `sh <remote>` on the **same** SSH connection), `agent_transfer` (source `host` → cloud object → `agent_transfer.dest_host`; same A→cloud→B flow as the web UI; optional `cloud_backend_ref` needs `--config` / `HONEY_CONFIG` like the files API), or `ai` (terminal local summarizer after prior steps; `host` must be `"_"`; `OPENAI_API_KEY` when executing; optional step-level `notify { ... }` with `HONEY_NOTIFY_*` env for HTTP, Slack webhook, or Telegram — see `honey cue-exec` docs). Optional `run_as` applies to `command` and `script` runs (not to SFTP-only `put`/`get`, `agent_transfer`, or `ai`). Optional `recipe.defaults.env` and per-step `env` are `export`’d on the remote before the command or script (step overrides duplicate keys from defaults); `env` is not supported on `put`/`get`/`agent_transfer`/`ai`. Optional **`recipe.defaults.ssh_port`** and per-step **`ssh_port`** (1–65535) override the **numeric TCP port** for SSH-backed steps; precedence is **step `ssh_port` → `defaults.ssh_port` → host row `meta.ssh_port` → `~/.ssh/config` `Port` → 22** (invalid or missing values fall through). For **`agent_transfer`**, prefer **`meta.ssh_port`** on the source and destination rows; recipe-level `ssh_port` applies to normal single-host steps. **Example recipes** live under [`examples/recipe/`](https://github.com/shareed2k/honey/tree/main/examples/recipe) — see that folder’s [`README.md`](https://github.com/shareed2k/honey/blob/main/examples/recipe/README.md) for a table of files (including `file_transfer.cue`, `script_step.cue`, `with_env.cue`, `agent_transfer.cue`, `high_load_processes.cue`, `postgres_replica_lag.cue`, `postgres_logical_replication_slots.cue`, `ai_summarize_hosts.cue`).

```bash
./honey cue-validate examples/recipe/recipe.cue
```

The document must include a top-level `recipe` field. Implementation: [`cuelang.org/go`](https://github.com/cue-lang/cue) v0.12 (`internal/cuetry`).

From the **search TUI**, **r** runs a recipe against **marked `*` rows (with IP) or all with IP if nothing is marked** — same scope as parallel **e** (dry-run unless the path ends with `!`). **`cue-exec`** on the CLI runs the same search as `honey search` (all search flags apply), resolves each step’s `host` using the result set: **exact name** match (case-insensitive), a **literal IP**, **`host: "*"`** to run on **every** matching row with a **PrimaryIP**, or **`host: "re:PATTERN"`** for a **Go regexp** (RE2) matched against each row’s **Name** (again only rows with an IP). Each step runs **in parallel** across targets: shell via SSH; **SFTP** for `put` / `get`; **`script`** uploads then runs in one session per host; **`agent_transfer`** runs once per step (source and `dest_host` must each match **exactly one** row). Relative `local` paths are resolved from the **recipe file’s directory**. For **`get`** with **multiple** targets, `local` must be a **directory** (trailing `/` or an existing folder); files are written as `<dir>/<sanitized_host>_<basename(remote)>`. It prints a **dry-run plan** by default and only runs when you pass **`--execute`**. Use `(?i)` inside regex patterns for case-insensitive matching. Optional `recipe.defaults.run_as` or per-step `run_as` applies to **`command` and `script`** runs (`sudo -n -u <user> -- sh -lc '...'`). Optional `defaults.env` / step `env` apply to those same runs.

```bash
# Plan only (safe default)
./honey cue-exec examples/recipe/recipe.cue my-name-filter

# Same as search: backends, --name, --ssh-user, etc.
./honey cue-exec --backends gcp-prod-us2 examples/recipe/recipe.cue

# Actually run each step over SSH
./honey cue-exec --execute examples/recipe/recipe.cue

# Extra remote env for command/script steps (repeat -e or --env; overrides recipe keys)
./honey cue-exec -e FOO=bar --env BAZ=qux examples/recipe/with_env.cue my-filter
```

### TUI keys

- **Enter**: interactive shell on the **selected** row—`ssh <user>@<ip>` for VMs (user from `--ssh-user`, default `$USER`), **k8s pod exec** for pods, **`docker exec`** TTY for docker container/swarm rows with `container_id`. Uses the system `ssh` binary where applicable (`~/.ssh/config` honored).
- **t**: enter `-L` spec (e.g. `8080:localhost:8080`), then **Enter** to run `ssh -L ... user@ip` on the selected host (not supported for pure docker container rows)
- **x**: toggle a `*` mark on the current table row (for parallel **e** / recipes). The first column shows `*` for marked rows.
- **Ctrl+a**: mark all **executable** rows (IP, k8s pod, or docker container with `container_id`; replaces the previous mark set).
- **c**: clear all `*` marks.
- **e**: run the **same** remote shell command in parallel: **only** on marked executable rows; if **nothing** is marked, on **every** executable row in the table. SSH targets use [goph](https://github.com/melbahja/goph) (`golang.org/x/crypto/ssh`) with **known_hosts** checking; docker rows use **`docker exec`**. Auth from **ssh-agent** (`SSH_AUTH_SOCK`), `IdentityFile` entries from `~/.ssh/config` for the host/IP honey dials, optional comma-separated **`HONEY_SSH_IDENTITY_FILES`** (extra private key paths), then default keys under `~/.ssh`: `id_ed25519`, `id_rsa`, `id_ecdsa`, `google_compute_engine` (GCE), `id_dsa` if present. **`Match` in `~/.ssh/config`** is honored when **`ssh` is on `PATH`** (honey uses `ssh -G`); set **`HONEY_SSH_OPENSSH_G=0`** to use the built-in parser only (**`Match` ignored**—duplicate `IdentityFile` under a plain `Host` if needed). Non-interactive; one host failing does not stop the others. The command prompt shows the current scope; results include a short scope line. **Esc** from the prompt returns to the table; **Esc** from results returns to the table; **q** / **Ctrl+C** quits without opening a single-host session.
- **r**: run a **CUE recipe** (same as `honey cue-exec`) against a **chosen subset** of the table: **only `*`‑marked executable rows** if you marked any rows; **otherwise every executable row** (same scope as **e**). **No second search.** Append `!` to the recipe path to execute for real; without `!` it is a dry-run plan. Uses the same `--ssh-user` as the table.
- **q** / **Ctrl+C**: quit without SSH (from the table or from the parallel-results view)

Parallel SSH (**e**), CUE recipes, and **`cue-exec`** share the same in-process host-key check (`~/.ssh/known_hosts`, etc.). **By default**, if the server host key changed (e.g. VM rebuild), honey **rewrites writable known_hosts files** (in-process, same idea as `ssh-keygen -R`) and appends the new key instead of failing. Set **`HONEY_SSH_RENEW_STALE_HOST_KEYS=0`** to turn that off and require manual `ssh-keygen -R <host>` on mismatch.

**Programmatic SSH** (parallel **e**, web terminal, agent transfer, `cue-exec`) prefers resolving `~/.ssh/config` with the system **`ssh -G`** when **`ssh` is on `PATH`**, so **`Match`** is evaluated the same way as OpenSSH. Set **`HONEY_SSH_OPENSSH_G=0`** to use honey’s built-in parser only (no subprocess; **`Match` is ignored**—duplicate settings under a plain `Host` entry if needed). Resolved settings include `User`, `HostName`, `Port`, `IdentityFile`, `ProxyJump`, and host-key-related paths. When a host row includes **`meta.ssh_port`** (valid 1–65535), honey uses that **leaf TCP port** for goph-based dials, web SSH, tunnels, and inventory **`ansible_port`**, still merging the rest from `~/.ssh/config`. **`cue-exec`** can also set **`recipe.defaults.ssh_port`** / per-step **`ssh_port`** (see the CUE section above for precedence vs `meta.ssh_port` and `~/.ssh/config`). If `ssh user@ip` works but honey does not, add **`IdentityFile`** for that **IP** (or set **`HONEY_SSH_IDENTITY_FILES=/path/to/key`** with comma-separated paths), ensure **`ssh-agent`** has your key, or rely on a default filename under `~/.ssh/` (including **`google_compute_engine`** for Google Cloud).

### Provider auth / flags

Per-provider setup (minimal auth, YAML, and CLI flags):

- **Example configs:** [`examples/config/`](examples/config/) (one YAML per provider)
- **Docs site:** [Providers](https://shareed2k.github.io/honey/providers/) — or [`website/docs/providers/index.md`](website/docs/providers/index.md) in the repo
- **Quick index:** [GCP](website/docs/providers/gcp.md) · [AWS](website/docs/providers/aws.md) · [Kubernetes](website/docs/providers/kubernetes.md) · [Consul](website/docs/providers/consul.md) · [Proxmox](website/docs/providers/proxmox.md) · [TrueNAS](website/docs/providers/truenas.md) · [Local](website/docs/providers/local.md) · [Docker](website/docs/providers/docker.md)

| Provider | Search ID | Minimal auth (summary) |
|----------|-----------|-------------------------|
| GCP | `gcp` | Application Default Credentials; `roles/compute.viewer` (or `compute.instances.list`) |
| AWS | `aws` | AWS profile / credential chain; `ec2:DescribeInstances` |
| Kubernetes | `k8s` | kubeconfig + context; `get nodes` (pods mode: `get pods`, ephemeral debug) |
| Consul | `consul` | HTTP API; ACL token if enabled |
| Proxmox | `proxmox` | API URL + password or API token; VM read (`PVEVMRO`) |
| TrueNAS | `truenas` | SCALE 25.04+ API key |
| Local | `local` | None (static list) |
| Docker | `docker` | Engine API (socket, `DOCKER_HOST`, or SSH hop) |

If a provider is unreachable, the command fails (use `--provider` to narrow scope).

#### Kubernetes pods (`--k8s-mode pods`)

When searching for Kubernetes pods (`--provider k8s --k8s-mode pods`), `honey` provides advanced, transparent execution capabilities without needing any server daemons:

1. **Direct Pod Exec:** Honey talks directly to the Kubernetes API to spawn an interactive shell or run commands inside the pod's primary container.
2. **Ephemeral Containers:** To avoid permission issues (like read-only root filesystems), `honey` injects a lightweight, short-lived `alpine` Ephemeral Container (`honey-debug-*`) into the target pod. This container shares the process and filesystem namespace but has its own writable overlay.
3. **Transparent File Transfers:** CUE `put` and `get` operations, as well as `script` step uploads, are implemented securely by dynamically streaming `tar` archives over the `exec` connection into the ephemeral container (similar to `kubectl cp`). No SFTP server required!
4. **Seamless Experience:** Your interactive sessions, parallel commands, and CUE recipes work identically to actual SSH nodes, preserving context, streams, and file permissions, completely daemonless.

## Layout

- `cmd/honey` — CLI entrypoint (`search`, `inventory`, `backends`, `mcp`, …)
- `internal/cli` — Cobra flags and wiring
- `internal/inventory` — Ansible JSON mapping from `hosts.Record`
- `contrib/ansible` — Ansible inventory plugin (`inventory_plugins/honey.py`) + example YAML
- `internal/mcpserver` — MCP tool handlers
- `internal/searchrun` — shared search + provider wiring
- `internal/config` — optional YAML (`backends`, `defaults`)
- `internal/hosts` — `Record`, `Query`, cache, parallel orchestration
- `internal/provider/*` — GCP, AWS, k8s, Consul, Docker, Proxmox, local integrations
- `internal/ui` — Bubble Tea table + SSH actions
- `internal/cuetry` — CUE validation + decode for remote recipes (`cue-validate`, `cue-exec`)
- `website/docs/add-new-backend.md` — contributor guide for adding a new backend (Docusaurus source)
- `website/docs/web-ui.md` — web UI, API, session recording, file transfer, AI assist, and prebuilt transfer agent downloads (Docusaurus source)

## Web UI (`honey web`)

Embedded **loopback-only** web server with a random bearer token (override with `HONEY_WEB_TOKEN`). Serves a React UI for backends list, search, provider/backend filters, YAML config edit, structured **backends** CRUD, browser terminal (**SSH**, **Kubernetes** exec TTY, and **Docker** `exec` attach), optional **session recording** (`--record-dir`), local/remote **file browser** and **agent-based** cloud transfer (`honey-transfer-agent`), **CUE recipe** run/view, and optional **AI assist** for the terminal and recipes when **`OPENAI_API_KEY`** is set (optional **`OPENAI_BASE_URL`** for compatible gateways or local inference).

**Full documentation:** published site [Web UI & AI assist](https://honey.shareed2k.win/web-ui) (built from [`website/docs/web-ui.md`](https://github.com/shareed2k/honey/blob/main/website/docs/web-ui.md)).

```bash
# One-time: build UI assets into internal/webserver/static (CI runs this automatically)
make webui

go build -o honey ./cmd/honey
./honey web --listen 127.0.0.1:8765 --config ~/.config/honey/config.yaml

# Optional Prometheus metrics on a separate loopback port (no auth)
./honey web --listen 127.0.0.1:8765 --metrics-listen 127.0.0.1:9091
```

Optional flags include `--record-dir` (session recordings), `--files-root` (file browser root; defaults to `$HONEY_FILES_ROOT` or `$HOME`), `--agent-bin` / `--agent-build-cache-dir` for the transfer agent, and **`--metrics-listen`** (loopback-only Prometheus scrape endpoint at `/metrics`, separate from the token-protected UI). When the server cannot `go build` the agent (no checkout), Honey **downloads** prebuilt `honey-transfer-agent` from the **same GitHub release tag as this `honey` binary** (`…/releases/download/<vTAG>/honey-transfer-agent-<goos>-<goarch>`; dev builds use `…/latest/download/…`). Override with **`HONEY_TRANSFER_AGENT_DOWNLOAD_BASE`** or **`HONEY_TRANSFER_AGENT_DOWNLOAD_URL`**, or disable the default with **`HONEY_TRANSFER_AGENT_DOWNLOAD_DISABLE_DEFAULT=1`** (see `website/docs/web-ui.md`).

Open the **URL printed on stderr** (includes `?token=…`). The web UI **API** tab embeds **Swagger UI** against the same OpenAPI document as **`GET /api/v1/openapi.json`** (same auth as other routes); regenerate the spec with **`make openapi`** or `go generate` in `internal/webserver` after changing handler comments. Deep-link the tab with **`?tab=api-docs`**. Notable API routes: `GET /api/v1/meta` (includes `terminal_assist_available`, `session_recording_available`, and `metrics_url` when `--metrics-listen` is set), `POST /api/v1/search`, `GET`/`PUT /api/v1/config`, structured backends under `/api/v1/config/backends/…` (path segment **`kubernetes`** matches YAML; search uses provider id **`k8s`**), `POST /api/v1/upload`, **`GET /api/v1/terminal-assist/models`** and **`POST /api/v1/terminal-assist`** (terminal AI), **`POST /api/v1/recipes/assist`** (recipe AI), recordings under `/api/v1/recordings`, WebSocket **`GET /ws/ssh?token=…`**. Authenticate with `Authorization: Bearer <token>` or `X-Honey-Token`.

**Local UI dev** (Vite proxies to the Go server): run `honey web` on `8765`, then `cd webui && npm install && npm run dev` and open Vite’s URL.

## Tests

```bash
go test ./...
```

## License

This project is released under the [MIT License](https://github.com/shareed2k/honey/blob/main/LICENSE).
