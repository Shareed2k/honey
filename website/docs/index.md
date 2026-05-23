---
id: index
title: Honey Documentation
slug: /
---

CLI to search **GCP Compute Engine**, **AWS EC2**, **Kubernetes** (nodes or pods), **Consul** catalog nodes, and **Proxmox VE** instances **in parallel**, optionally cache results, then use a **terminal UI** to SSH or open an **SSH local forward** (`-L`) via the system `ssh` binary.

## Prerequisites

- Go 1.26.2+ (see `go` directive in `go.mod`; use this toolchain or newer so `govulncheck` reports clean stdlib fixes from Go 1.26.1/1.26.2)
- Credentials for each backend you enable — see **[Providers](./providers)** for minimal auth and YAML per backend (GCP, AWS, Kubernetes, Consul, Proxmox, TrueNAS, local, Docker)

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
```

## Guides

- [Docker auto-discover on cloud VMs](./docker-auto-discover.md) — find containers on GCP/AWS instances with `HONEY_FEATURE_DOCKER_VIA_PROVIDERS=1`
- [Web UI & AI assist](./web-ui.md)
- [CUE recipes](./cue-recipes.md)

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

`honey inventory` runs the **same discovery** as `honey search` (all search flags: `--config`, `--provider`, `--backends`, name filters, cache flags, per-provider auth, and so on) and prints **Ansible script-style JSON**: a `honey` group, optional `honey_provider_*`, `honey_region_*`, `honey_zone_*` groups, and `_meta.hostvars`. Each host gets `ansible_host` from the discovered primary IP when present, `ansible_user` from `--ssh-user` (or `defaults.ssh_user` in YAML), plus `honey_*` fields and `honey_meta_<key>` from each record’s `meta` map.

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

Validate and run playbook-shaped [CUE](https://cuelang.org/) recipes against search results (`cue-validate`, `cue-exec`, TUI **r**, Web UI Recipes tab). Steps support `command`, `put`, `get`, `script`, `agent_transfer`, `ai`, and `plugin`; optional **graph mode** (`type: "graph"`, `id`, `depends`), **conditional `when`** expressions ([CEL](https://github.com/google/cel-spec)), `env_from`, and shared **KV tunnel**.

**Full guide:** [CUE Recipes](./cue-recipes.md) · **WASM plugins:** [Plugin development](./plugins-development.md) · **Examples:** [`examples/recipe/`](https://github.com/shareed2k/honey/tree/main/examples/recipe) ([README](https://github.com/shareed2k/honey/blob/main/examples/recipe/README.md))

```bash
./honey cue-validate examples/recipe/recipe.cue
```

The document must include a top-level `recipe` field. Implementation: [`cuelang.org/go`](https://github.com/cue-lang/cue) v0.12 (`internal/cuetry`).

From the **search TUI**, **r** runs a recipe against **marked `*` rows (with IP) or all with IP if nothing is marked** — same scope as parallel **e** (dry-run unless the path ends with `!`). **`cue-exec`** on the CLI runs the same search as `honey search` (all search flags apply), resolves each step’s `host` using the result set: **exact name** match (case-insensitive), a **literal IP**, **`host: "*"`** to run on **every** matching row with a **PrimaryIP**, or **`host: "re:PATTERN"`** for a **Go regexp** (RE2) matched against each row’s **Name** (again only rows with an IP). Each step runs **in parallel** across targets: shell via SSH; **SFTP** for `put` / `get`; **`script`** uploads then runs in one session per host; **`agent_transfer`** emits one result per step (source and `dest_host` must each match **exactly one** row). Relative `local` paths are resolved from the **recipe file’s directory**. For **`get`** with **multiple** targets, `local` must be a **directory** (trailing `/` or an existing folder); files are written as `<dir>/<sanitized_host>_<basename(remote)>`. It prints a **dry-run plan** by default and only runs when you pass **`--execute`**. Use `(?i)` inside regex patterns for case-insensitive matching. Optional `recipe.defaults.run_as` or per-step `run_as` applies to **`command` and `script`** runs (`sudo -n -u <user> -- sh -lc '...'`). Optional `defaults.env` / step `env` apply to those same runs.

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

- **Enter**: `ssh <user>@<ip>` (user from `--ssh-user`, default `$USER`) for the **selected** row
- **t**: enter `-L` spec (e.g. `8080:localhost:8080`), then **Enter** to run `ssh -L ... user@ip` on the selected host
- **x**: toggle a `*` mark on the current table row (for parallel SSH only). The first column shows `*` for marked rows.
- **Ctrl+a**: mark all rows that have an IP (replaces the previous mark set).
- **c**: clear all `*` marks.
- **e**: run the **same** remote shell command in parallel via [goph](https://github.com/melbahja/goph) (`golang.org/x/crypto/ssh`): **only** on marked rows that have an IP; if **nothing** is marked, it runs on **every** listed host that has an IP. **known_hosts** host-key checking; auth from **ssh-agent** (`SSH_AUTH_SOCK`), `IdentityFile` entries from `~/.ssh/config` for the host/IP honey dials, optional comma-separated **`HONEY_SSH_IDENTITY_FILES`** (extra private key paths), then default keys under `~/.ssh`: `id_ed25519`, `id_rsa`, `id_ecdsa`, `google_compute_engine` (GCE), `id_dsa` if present. **`Match` blocks in `~/.ssh/config`** are honored when the system **`ssh` binary** is on `PATH` (honey resolves config via `ssh -G`); set **`HONEY_SSH_OPENSSH_G=0`** to force the built-in parser only (no `Match`). If you disable `ssh -G`, duplicate needed `IdentityFile` lines under a plain `Host` entry if honey connects by IP. Non-interactive; one host failing does not stop the others. The command prompt shows the current scope; results include a short scope line. **Esc** from the prompt returns to the table; **Esc** from results returns to the table; **q** / **Ctrl+C** quits without opening a single-host SSH session. (Single-host **Enter** / **t** still use the system `ssh` binary, including `~/.ssh/config`.)
- **r**: run a **CUE recipe** (same as `honey cue-exec`) against a **chosen subset** of the table: **only `*`‑marked rows that have an IP** if you marked any rows; **otherwise every row that has an IP** (same scope as **e**). **No second search.** Append `!` to the recipe path to execute for real; without `!` it is a dry-run plan. Uses the same `--ssh-user` as the table.
- **q** / **Ctrl+C**: quit without SSH (from the table or from the parallel-results view)

Parallel SSH (**e**), CUE recipes, and **`cue-exec`** share the same in-process host-key check (`~/.ssh/known_hosts`, etc.). **By default**, if the server host key changed (e.g. VM rebuild), honey **rewrites writable known_hosts files** (in-process, same idea as `ssh-keygen -R`) and appends the new key instead of failing. Set **`HONEY_SSH_RENEW_STALE_HOST_KEYS=0`** to turn that off and require manual `ssh-keygen -R <host>` on mismatch.

**Programmatic SSH** (parallel **e**, web terminal, agent transfer, `cue-exec`) prefers resolving `~/.ssh/config` with the system **`ssh -G`** when **`ssh` is on `PATH`**, so **`Match`** is evaluated like OpenSSH. Set **`HONEY_SSH_OPENSSH_G=0`** for honey’s built-in parser only (**`Match` ignored**). When a host row includes **`meta.ssh_port`** (valid 1–65535), honey uses that **leaf TCP port** for programmatic dials and inventory **`ansible_port`**, still merging other settings from `~/.ssh/config`. If `ssh user@ip` works but honey does not, add **`IdentityFile`** for that **IP** (or set **`HONEY_SSH_IDENTITY_FILES=/path/to/key`** with comma-separated paths), ensure **`ssh-agent`** has your key, or rely on a default filename under `~/.ssh/` (including **`google_compute_engine`** for Google Cloud).

### Provider auth / flags

| Provider | Auth / config |
|----------|----------------|
| **GCP** | Application Default Credentials; set `GOOGLE_CLOUD_PROJECT` or `GCP_PROJECT`, or pass `--gcp-project`. Optional `--gcp-zone` (default: all zones, aggregated list). |
| **AWS** | Default credential chain; `--aws-profile`, `--aws-region`. |
| **Kubernetes** | Current kubeconfig; `--kube-context`, `--kubeconfig`, `--k8s-mode=nodes` (default) or `pods`. For pods, `honey` seamlessly utilizes Kubernetes `exec` directly without needing SSH or SFTP. |

...

When searching for Kubernetes pods (`--provider k8s --k8s-mode pods`), `honey` provides advanced, transparent execution capabilities without needing any server daemons:

...

2. **Ephemeral Containers:** To avoid permission issues (like read-only root filesystems), `honey` injects a lightweight, short-lived `alpine` Ephemeral Container (`honey-debug-*`) into the target pod. This container shares the process and filesystem namespace but has its own writable overlay.
3. **Transparent File Transfers:** CUE `put` and `get` operations, as well as `script` step uploads, are implemented securely by dynamically streaming `tar` archives over the `exec` connection into the ephemeral container (similar to `kubectl cp`). No SFTP server required!
4. **Seamless Experience:** Your interactive sessions, parallel commands, and CUE recipes work identically to actual SSH nodes, preserving context, streams, and file permissions, completely daemonless.
| **Consul** | `CONSUL_HTTP_ADDR` or `--consul-addr`; `--consul-datacenter`, `--consul-token` / `CONSUL_HTTP_TOKEN`. |
| **Proxmox** | `--proxmox-url` (e.g. `https://10.0.0.1:8006/api2/json`); Auth via `--proxmox-user` / `--proxmox-password` OR `--proxmox-token-id` / `--proxmox-token-secret`. Add `--proxmox-insecure` to bypass TLS verification. Both LXC and QEMU (VM) types are fully supported.<br /><br />**Token Creation Example**: Proxmox requires the `PVEVMRO` (Read Only) role to list VMs and fetch networking information. <br />1. Log into your Proxmox web UI.<br />2. Navigate to **Datacenter** > **Permissions** > **API Tokens**.<br />3. Click **Add** and select your User (e.g., `root@pam`), name the token `honey`.<br />4. Uncheck **Privilege Separation** if you want the token to inherit full user privileges, OR assign the `PVEVMRO` role to `/vms` explicitly.<br />5. Copy the Secret ID.<br />Your `token_id` in the YAML config will be formatted exactly as `user@realm!tokenname` (e.g. `root@pam!honey`). |

If a provider is unreachable, the command fails (use `--provider` to narrow scope).

## Embedded web UI (`honey web`)

Loopback HTTP server with bearer token auth (see the repository **README** for `make webui`, listen address, and `HONEY_WEB_TOKEN`). The bundled UI covers search (with provider/backend dropdowns), raw YAML config, structured **backends** JSON CRUD under `/api/v1/config/backends/…`, and a WebSocket terminal on `/ws/ssh` for **SSH** hosts and **Kubernetes pods** (ephemeral container exec). REST path segments use YAML keys such as **`kubernetes`**; search filters still use the provider id **`k8s`**.

## Layout

- `cmd/honey` — CLI entrypoint (`search`, `inventory`, `backends`, `mcp`, …)
- `internal/cli` — Cobra flags and wiring
- `internal/inventory` — Ansible JSON mapping from `hosts.Record`
- `contrib/ansible` — Ansible inventory plugin (`inventory_plugins/honey.py`) + example YAML
- `internal/mcpserver` — MCP tool handlers
- `internal/searchrun` — shared search + provider wiring
- `internal/config` — optional YAML (`backends`, `defaults`)
- `internal/hosts` — `Record`, `Query`, cache, parallel orchestration
- `internal/provider/*` — GCP, AWS, k8s, Consul integrations
- `internal/ui` — Bubble Tea table + SSH actions
- `internal/webserver` — embedded `honey web` UI (static SPA + REST + WebSocket terminal)
- `internal/cuetry` — CUE validation + decode for remote recipes (`cue-validate`, `cue-exec`)
- `internal/plugins` — Extism WASM plugins (`cue_transform`, custom steps, secret backends)
- `pkg/pluginpdk` — Go helpers for plugin authors (recipe KV, etc.)

## Tests

```bash
go test ./...
```

## License

This project is released under the [MIT License](https://github.com/shareed2k/honey/blob/main/LICENSE).
