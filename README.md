# honey

CLI to search **GCP Compute Engine**, **AWS EC2**, **Kubernetes** (nodes or pods), **Consul** catalog nodes, and **Proxmox VE** instances **in parallel**, optionally cache results, then use a **terminal UI** to SSH or open an **SSH local forward** (`-L`) via the system `ssh` binary.

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
```

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

# Cache (default TTL 1m); force refresh
./honey search --refresh foo
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

**Local Ansible:** Ansible’s **script** inventory expects an **executable file** on disk. Use a tiny wrapper that `exec`s honey, make it executable, then pass it to `-i`, for example:

```bash
printf '%s\n' '#!/bin/sh' 'exec /path/to/honey inventory "$@"' > honey-ansible-inv && chmod +x honey-ansible-inv
ansible-playbook -i ./honey-ansible-inv site.yml
```

Add fixed flags inside the wrapper if you want (for example `exec /path/to/honey inventory --config "$HOME/.config/honey/config.yaml" "$@"`).

**CI / AWX / Ansible Tower:** install the `honey` binary on the execution environment and supply credentials the same way you would for `honey search` (for example `HONEY_CONFIG` pointing at a mounted secret, `GOOGLE_APPLICATION_CREDENTIALS`, `AWS_PROFILE` / instance role, `KUBECONFIG`, `CONSUL_HTTP_TOKEN`, or Proxmox flags baked into the wrapper or injected via environment). Configure a **custom inventory script** (or equivalent) that runs `honey inventory` with the same arguments Ansible would pass to a dynamic inventory script (`--list` or `--host <name>`). Honey does **not** replace Tower or AWX; it only **feeds inventory JSON** from live APIs.

### CUE recipes (experimental)

Validate a playbook-shaped [CUE](https://cuelang.org/) file: each step has `host` and **exactly one** of `command`, `put` (SFTP upload), `get` (SFTP download), `script` (upload `local` → `remote`, then run `sh <remote>` on the **same** SSH connection), or `agent_transfer` (source `host` → cloud object → `agent_transfer.dest_host`; same A→cloud→B flow as the web UI; optional `cloud_backend_ref` needs `--config` / `HONEY_CONFIG` like the files API). Optional `run_as` applies to `command` and `script` runs (not to SFTP-only `put`/`get` or `agent_transfer`). Optional `recipe.defaults.env` and per-step `env` are `export`’d on the remote before the command or script (step overrides duplicate keys from defaults); `env` is not supported on `put`/`get`/`agent_transfer`. **Example recipes** live under [`examples/recipe/`](https://github.com/shareed2k/honey/tree/main/examples/recipe) — see that folder’s [`README.md`](https://github.com/shareed2k/honey/blob/main/examples/recipe/README.md) for a table of files (including `file_transfer.cue`, `script_step.cue`, `with_env.cue`, `agent_transfer.cue`).

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

- **Enter**: `ssh <user>@<ip>` (user from `--ssh-user`, default `$USER`) for the **selected** row
- **t**: enter `-L` spec (e.g. `8080:localhost:8080`), then **Enter** to run `ssh -L ... user@ip` on the selected host
- **x**: toggle a `*` mark on the current table row (for parallel SSH only). The first column shows `*` for marked rows.
- **Ctrl+a**: mark all rows that have an IP (replaces the previous mark set).
- **c**: clear all `*` marks.
- **e**: run the **same** remote shell command in parallel via [goph](https://github.com/melbahja/goph) (`golang.org/x/crypto/ssh`): **only** on marked rows that have an IP; if **nothing** is marked, it runs on **every** listed host that has an IP. **known_hosts** host-key checking; auth from **ssh-agent** (`SSH_AUTH_SOCK`) and keys under `~/.ssh` (`id_ed25519`, `id_rsa`, `id_ecdsa`). Non-interactive; one host failing does not stop the others. The command prompt shows the current scope; results include a short scope line. **Esc** from the prompt returns to the table; **Esc** from results returns to the table; **q** / **Ctrl+C** quits without opening a single-host SSH session. (Single-host **Enter** / **t** still use the system `ssh` binary, including `~/.ssh/config`.)
- **r**: run a **CUE recipe** (same as `honey cue-exec`) against a **chosen subset** of the table: **only `*`‑marked rows that have an IP** if you marked any rows; **otherwise every row that has an IP** (same scope as **e**). **No second search.** Append `!` to the recipe path to execute for real; without `!` it is a dry-run plan. Uses the same `--ssh-user` as the table.
- **q** / **Ctrl+C**: quit without SSH (from the table or from the parallel-results view)

Parallel SSH (**e**), CUE recipes, and **`cue-exec`** share the same in-process host-key check (`~/.ssh/known_hosts`, etc.). **By default**, if the server host key changed (e.g. VM rebuild), honey **rewrites writable known_hosts files** (in-process, same idea as `ssh-keygen -R`) and appends the new key instead of failing. Set **`HONEY_SSH_RENEW_STALE_HOST_KEYS=0`** to turn that off and require manual `ssh-keygen -R <host>` on mismatch.

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

## Layout

- `cmd/honey` — CLI entrypoint (`search`, `inventory`, `backends`, `mcp`, …)
- `internal/cli` — Cobra flags and wiring
- `internal/inventory` — Ansible JSON mapping from `hosts.Record`
- `internal/mcpserver` — MCP tool handlers
- `internal/searchrun` — shared search + provider wiring
- `internal/config` — optional YAML (`backends`, `defaults`)
- `internal/hosts` — `Record`, `Query`, cache, parallel orchestration
- `internal/provider/*` — GCP, AWS, k8s, Consul integrations
- `internal/ui` — Bubble Tea table + SSH actions
- `internal/cuetry` — CUE validation + decode for remote recipes (`cue-validate`, `cue-exec`)
- `website/docs/add-new-backend.md` — contributor guide for adding a new backend (Docusaurus source)
- `website/docs/web-ui.md` — web UI, API, session recording, file transfer, AI assist, and prebuilt transfer agent downloads (Docusaurus source)

## Web UI (`honey web`)

Embedded **loopback-only** web server with a random bearer token (override with `HONEY_WEB_TOKEN`). Serves a React UI for backends list, search, provider/backend filters, YAML config edit, structured **backends** CRUD, browser terminal (**SSH** and **Kubernetes** exec TTY), optional **session recording** (`--record-dir`), local/remote **file browser** and **agent-based** cloud transfer (`honey-transfer-agent`), **CUE recipe** run/view, and optional **AI assist** for the terminal and recipes when **`OPENAI_API_KEY`** is set (optional **`OPENAI_BASE_URL`** for compatible gateways or local inference).

**Full documentation:** published site [Web UI & AI assist](https://honey.shareed2k.win/web-ui) (built from [`website/docs/web-ui.md`](https://github.com/shareed2k/honey/blob/main/website/docs/web-ui.md)).

```bash
# One-time: build UI assets into internal/webserver/static (CI runs this automatically)
make webui

go build -o honey ./cmd/honey
./honey web --listen 127.0.0.1:8765 --config ~/.config/honey/config.yaml
```

Optional flags include `--record-dir` (session recordings), `--files-root` (file browser root; defaults to `$HONEY_FILES_ROOT` or `$HOME`), and `--agent-bin` / `--agent-build-cache-dir` for the transfer agent. When the server cannot `go build` the agent (no checkout), Honey **downloads** prebuilt `honey-transfer-agent` from the **same GitHub release tag as this `honey` binary** (`…/releases/download/<vTAG>/honey-transfer-agent-<goos>-<goarch>`; dev builds use `…/latest/download/…`). Override with **`HONEY_TRANSFER_AGENT_DOWNLOAD_BASE`** or **`HONEY_TRANSFER_AGENT_DOWNLOAD_URL`**, or disable the default with **`HONEY_TRANSFER_AGENT_DOWNLOAD_DISABLE_DEFAULT=1`** (see `website/docs/web-ui.md`).

Open the **URL printed on stderr** (includes `?token=…`). Notable API routes: `GET /api/v1/meta` (includes `terminal_assist_available`, `session_recording_available`), `POST /api/v1/search`, `GET`/`PUT /api/v1/config`, structured backends under `/api/v1/config/backends/…` (path segment **`kubernetes`** matches YAML; search uses provider id **`k8s`**), `POST /api/v1/upload`, **`GET /api/v1/terminal-assist/models`** and **`POST /api/v1/terminal-assist`** (terminal AI), **`POST /api/v1/recipes/assist`** (recipe AI), recordings under `/api/v1/recordings`, WebSocket **`GET /ws/ssh?token=…`**. Authenticate with `Authorization: Bearer <token>` or `X-Honey-Token`.

**Local UI dev** (Vite proxies to the Go server): run `honey web` on `8765`, then `cd webui && npm install && npm run dev` and open Vite’s URL.

## Tests

```bash
go test ./...
```

## License

This project is released under the [MIT License](https://github.com/shareed2k/honey/blob/main/LICENSE).
