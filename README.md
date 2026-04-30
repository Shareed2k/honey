# hostctl

CLI to search **GCP Compute Engine**, **AWS EC2**, **Kubernetes** (nodes or pods), and **Consul** catalog nodes **in parallel**, optionally cache results, then use a **terminal UI** to SSH or open an **SSH local forward** (`-L`) via the system `ssh` binary.

## Prerequisites

- Go 1.26+ (see `go` directive in `go.mod`)
- Credentials for each backend you enable (see below)

After cloning, generate checksums:

```bash
cd hostctl
go mod tidy
```

## Build

```bash
go build -o hostctl ./cmd/hostctl
```

## MCP server (stdio)

`hostctl mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io/) server over **stdin/stdout** using the official [`go-sdk`](https://github.com/modelcontextprotocol/go-sdk). **Do not log to stdout** (only stderr); stdout is reserved for the JSON-RPC stream.

**Tools**

| Tool | Purpose |
|------|---------|
| `search_hosts` | Same parallel search as `hostctl search`; arguments mirror flags (snake_case JSON). Optional `config_path`; otherwise uses `HOSTCTL_CONFIG` / default paths. |
| `list_backends` | Returns configured backends from YAML (`kind`, `name`, `hint`). Requires a resolvable config file. |

**Cursor** (example `mcp.json` fragment):

```json
{
  "mcpServers": {
    "hostctl": {
      "command": "/absolute/path/to/hostctl",
      "args": ["mcp"],
      "env": {
        "HOSTCTL_CONFIG": "/absolute/path/to/hostctl.yaml"
      }
    }
  }
}
```

**LM Studio** ([MCP docs](https://lmstudio.ai/docs/app/mcp); app **0.3.17+**)

LM Studio uses the same `mcpServers` shape as Cursor. In the app: open the **Program** tab (right sidebar) → **Install** → **Edit `mcp.json`**, then merge a `hostctl` entry into the top-level `mcpServers` object (or create the file if it is empty).

Typical file locations:

- **macOS / Linux:** `~/.lmstudio/mcp.json`
- **Windows:** `%USERPROFILE%\.lmstudio\mcp.json`

Example (replace paths with your real `hostctl` binary and YAML config):

```json
{
  "mcpServers": {
    "hostctl": {
      "command": "/Users/you/bin/hostctl",
      "args": ["mcp"],
      "env": {
        "HOSTCTL_CONFIG": "/Users/you/.config/hostctl/config.yaml"
      }
    }
  }
}
```

If you already have other servers under `mcpServers`, add only the `"hostctl": { ... }` block inside that object—do not duplicate the outer `"mcpServers"` key. After saving, restart the chat or reload tools if LM Studio does not pick up the server immediately. Enable or allow the **hostctl** tools under **App settings → Tools & integrations** (wording may vary by version) if the UI asks for permission.

**OpenCode** ([MCP servers](https://opencode.ai/docs/mcp-servers/), [config](https://opencode.ai/docs/config/))

OpenCode uses a top-level **`mcp`** object (not `mcpServers`). Local stdio servers use **`type": "local"`** and **`command`** as an **array** of executable + args. Environment variables go under **`environment`** (not `env`).

Merge this into `~/.config/opencode/opencode.json` (global) or a project **`opencode.json` / `opencode.jsonc`**—see OpenCode’s config precedence docs.

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "hostctl": {
      "type": "local",
      "command": ["/absolute/path/to/hostctl", "mcp"],
      "enabled": true,
      "environment": {
        "HOSTCTL_CONFIG": "/absolute/path/to/hostctl.yaml"
      }
    }
  }
}
```

Tools from this server appear with the **`hostctl_`** prefix (e.g. `hostctl_search_hosts`). You can mention `use hostctl` or a specific tool name in prompts; see OpenCode’s MCP docs for disabling servers or scoping tools per agent.

## Config file (optional)

You can define **multiple backends** per provider (for example two GCP projects or two Consul clusters) and optional **defaults**. If the file omits `backends` or leaves every backend list empty, behavior matches the flag-only mode (one implicit backend per provider).

Each list entry may set **`name`** (any stable string). Use **`--backends`** with a comma-separated list of those names (case-insensitive) to run only those entries—for example only `gcp-prod-us2` and `k8s-stg2`. Unnamed backends are skipped when `--backends` is set. Combine with **`--provider`** to further narrow by type (`gcp`, `k8s`, …).

**Lookup order** (first match wins):

1. `--config /path/to/file.yaml`
2. `HOSTCTL_CONFIG`
3. `$XDG_CONFIG_HOME/hostctl/config.yaml`
4. `~/.config/hostctl/config.yaml` when `XDG_CONFIG_HOME` is unset
5. `~/.hostctl.yaml`

**Precedence:** CLI flags override config `defaults` when you pass the flag (Cobra “changed” semantics). Query flags (`--gcp-project`, `--consul-addr`, etc.) override per-backend YAML values at search time.

Example `~/.config/hostctl/config.yaml`:

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
./hostctl search my-host

# JSON output, no TUI
./hostctl search --json my-host

# Explicit config path (otherwise see "Config file" below)
./hostctl search --config ~/.config/hostctl/config.yaml my-host

# Limit providers
./hostctl search --provider aws,k8s web

# Only specific named backends from config (see backends.*.name)
./hostctl search --backends gcp-team-a,k8s-staging web
./hostctl search --backends gcp-prod-us2 --provider gcp my-node

# Regex filter
./hostctl search --name-regex '^prod-'

# Cache (default TTL 1m); force refresh
./hostctl search --refresh foo
./hostctl search --no-cache foo
./hostctl search --cache-ttl 5m foo
```

### TUI keys

- **Enter**: `ssh <user>@<ip>` (user from `--ssh-user`, default `$USER`)
- **t**: enter `-L` spec (e.g. `8080:localhost:8080`), then **Enter** to run `ssh -L ... user@ip`
- **q** / **Ctrl+C**: quit without SSH

### Provider auth / flags

| Provider | Auth / config |
|----------|----------------|
| **GCP** | Application Default Credentials; set `GOOGLE_CLOUD_PROJECT` or `GCP_PROJECT`, or pass `--gcp-project`. Optional `--gcp-zone` (default: all zones, aggregated list). |
| **AWS** | Default credential chain; `--aws-profile`, `--aws-region`. |
| **Kubernetes** | Current kubeconfig; `--kube-context`, `--kubeconfig`, `--k8s-mode=nodes` (default) or `pods`. |
| **Consul** | `CONSUL_HTTP_ADDR` or `--consul-addr`; `--consul-datacenter`, `--consul-token` / `CONSUL_HTTP_TOKEN`. |

If a provider is unreachable, the command fails (use `--provider` to narrow scope).

## Layout

- `cmd/hostctl` — CLI entrypoint (`search`, `mcp`, …)
- `internal/cli` — Cobra flags and wiring
- `internal/mcpserver` — MCP tool handlers
- `internal/searchrun` — shared search + provider wiring
- `internal/config` — optional YAML (`backends`, `defaults`)
- `internal/hosts` — `Record`, `Query`, cache, parallel orchestration
- `internal/provider/*` — GCP, AWS, k8s, Consul integrations
- `internal/ui` — Bubble Tea table + SSH actions

## Tests

```bash
go test ./...
```
