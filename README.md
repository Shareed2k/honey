# honey

CLI to search **GCP Compute Engine**, **AWS EC2**, **Kubernetes** (nodes or pods), and **Consul** catalog nodes **in parallel**, optionally cache results, then use a **terminal UI** to SSH or open an **SSH local forward** (`-L`) via the system `ssh` binary.

## Prerequisites

- Go 1.26.2+ (see `go` directive in `go.mod`; use this toolchain or newer so `govulncheck` reports clean stdlib fixes from Go 1.26.1/1.26.2)
- Credentials for each backend you enable (see below)

After cloning, generate checksums:

```bash
cd honey
go mod tidy
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
| `search_hosts` | Same parallel search as `honey search`; arguments mirror flags (snake_case JSON). Optional `config_path`; otherwise uses `HONEY_CONFIG` (or legacy `HOSTCTL_CONFIG`) / default paths. |
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
3. `HOSTCTL_CONFIG` (legacy, same meaning)
4. `$XDG_CONFIG_HOME/honey/config.yaml`
5. `~/.config/honey/config.yaml` when `XDG_CONFIG_HOME` is unset
6. `~/.honey.yaml`
7. `~/.hostctl.yaml` (legacy filename)

**Precedence:** CLI flags override config `defaults` when you pass the flag (Cobra “changed” semantics). Query flags (`--gcp-project`, `--consul-addr`, etc.) override per-backend YAML values at search time.

Example `~/.config/honey/config.yaml`:

```yaml
version: 1
defaults:
  cache_ttl: 5m
  ssh_user: deploy
  k8s_mode: nodes
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
  consul:
    - name: consul-a
      addr: consul-a.internal:8500
    - name: consul-b-dc1
      addr: consul-b.internal:8500
      datacenter: dc1
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

### CUE recipes (experimental)

Validate a playbook-shaped [CUE](https://cuelang.org/) file: each step has `host` and **exactly one** of `command`, `put` (SFTP upload), `get` (SFTP download), or `script` (upload `local` → `remote`, then run `sh <remote>` on the **same** SSH connection). Optional `run_as` applies to `command` and `script` runs (not to SFTP-only `put`/`get`). **Example recipes** live under [`examples/recipe/`](examples/recipe/) — see that folder’s [`README.md`](examples/recipe/README.md) for a table of files (including `file_transfer.cue`, `script_step.cue`).

```bash
./honey cue-validate examples/recipe/recipe.cue
```

The document must include a top-level `recipe` field. Implementation: [`cuelang.org/go`](https://github.com/cue-lang/cue) v0.12 (`internal/cuetry`).

From the **search TUI**, **r** runs a recipe against **marked `*` rows (with IP) or all with IP if nothing is marked** — same scope as parallel **e** (dry-run unless the path ends with `!`). **`cue-exec`** on the CLI runs the same search as `honey search` (all search flags apply), resolves each step’s `host` using the result set: **exact name** match (case-insensitive), a **literal IP**, **`host: "*"`** to run on **every** matching row with a **PrimaryIP**, or **`host: "re:PATTERN"`** for a **Go regexp** (RE2) matched against each row’s **Name** (again only rows with an IP). Each step runs **in parallel** across targets: shell via SSH; **SFTP** for `put` / `get`; **`script`** uploads then runs in one session per host. Relative `local` paths are resolved from the **recipe file’s directory**. For **`get`** with **multiple** targets, `local` must be a **directory** (trailing `/` or an existing folder); files are written as `<dir>/<sanitized_host>_<basename(remote)>`. It prints a **dry-run plan** by default and only runs when you pass **`--execute`**. Use `(?i)` inside regex patterns for case-insensitive matching. Optional `recipe.defaults.run_as` or per-step `run_as` applies to **`command` and `script`** runs (`sudo -n -u <user> -- sh -lc '...'`).

```bash
# Plan only (safe default)
./honey cue-exec examples/recipe/recipe.cue my-name-filter

# Same as search: backends, --name, --ssh-user, etc.
./honey cue-exec --backends gcp-prod-us2 examples/recipe/recipe.cue

# Actually run each step over SSH
./honey cue-exec --execute examples/recipe/recipe.cue
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

### Provider auth / flags

| Provider | Auth / config |
|----------|----------------|
| **GCP** | Application Default Credentials; set `GOOGLE_CLOUD_PROJECT` or `GCP_PROJECT`, or pass `--gcp-project`. Optional `--gcp-zone` (default: all zones, aggregated list). |
| **AWS** | Default credential chain; `--aws-profile`, `--aws-region`. |
| **Kubernetes** | Current kubeconfig; `--kube-context`, `--kubeconfig`, `--k8s-mode=nodes` (default) or `pods`. |
| **Consul** | `CONSUL_HTTP_ADDR` or `--consul-addr`; `--consul-datacenter`, `--consul-token` / `CONSUL_HTTP_TOKEN`. |

If a provider is unreachable, the command fails (use `--provider` to narrow scope).

## Layout

- `cmd/honey` — CLI entrypoint (`search`, `backends`, `mcp`, …)
- `internal/cli` — Cobra flags and wiring
- `internal/mcpserver` — MCP tool handlers
- `internal/searchrun` — shared search + provider wiring
- `internal/config` — optional YAML (`backends`, `defaults`)
- `internal/hosts` — `Record`, `Query`, cache, parallel orchestration
- `internal/provider/*` — GCP, AWS, k8s, Consul integrations
- `internal/ui` — Bubble Tea table + SSH actions
- `internal/cuetry` — CUE validation + decode for remote recipes (`cue-validate`, `cue-exec`)

## Tests

```bash
go test ./...
```

## License

This project is released under the [MIT License](LICENSE).
