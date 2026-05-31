---
id: mcp-server
title: MCP Server
---

`honey mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io/) server over **stdin/stdout** using the official [`go-sdk`](https://github.com/modelcontextprotocol/go-sdk). This lets AI coding assistants (Cursor, LM Studio, OpenCode, and others) search your infrastructure and list backends as MCP tools.

## Tools

| Tool | Purpose |
|------|---------|
| `search_hosts` | Same parallel search as `honey search`; input fields mirror CLI flags (snake_case JSON). Optional `overrides` map for per-request provider settings. |
| `list_backends` | Returns configured backends from YAML (`kind`, `name`, `hint`). Requires a resolvable config file. |
| `exec_on_host` | Run a shell command on a host via SSH using its IP or hostname directly. Use `primary_ip` from a `search_hosts` result. |

## Cursor

Add a `honey` entry to `~/.cursor/mcp.json`:

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

If you already have other servers under `mcpServers`, add only the `"honey": { ... }` block — do not duplicate the outer key.

## LM Studio

LM Studio 0.3.17+ uses the same `mcpServers` shape as Cursor. In the app: **Program** tab (right sidebar) → **Install** → **Edit `mcp.json`**, then merge the `honey` entry:

| Platform | Config file |
|----------|------------|
| macOS / Linux | `~/.lmstudio/mcp.json` |
| Windows | `%USERPROFILE%\.lmstudio\mcp.json` |

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

After saving, restart the chat or reload tools. Enable **honey** tools under **App settings → Tools & integrations** if prompted.

## OpenCode

OpenCode uses a top-level `mcp` object (not `mcpServers`). Local stdio servers use `"type": "local"` and `command` as an array. Environment variables go under `environment` (not `env`).

Merge this into `~/.config/opencode/opencode.json` (global) or a project `opencode.json`:

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

Tools appear with the `honey_` prefix (e.g. `honey_search_hosts`). See [OpenCode MCP docs](https://opencode.ai/docs/mcp-servers/) for scoping tools per agent.

## Tool reference

### `search_hosts`

```json
{
  "name": "postgres",
  "name_regex": "",
  "providers": "gcp,k8s",
  "backends": "",
  "no_cache": false,
  "refresh": false,
  "config_path": "",
  "overrides": {
    "gcp": { "project": "my-project", "zone": "us-central1-a" },
    "k8s": { "context": "prod-cluster", "mode": "pods" }
  }
}
```

`overrides` keys match the provider ID (`gcp`, `aws`, `k8s`, `consul`, `proxmox`, `truenas`, `docker`). Fields inside each key mirror that provider's YAML backend config. Override precedence: `overrides` → YAML config value → CLI flag default.

Output:

```json
{
  "records": [
    { "name": "pg-primary", "primary_ip": "10.0.0.5", "provider": "gcp",
      "meta": { "zone": "us-central1-a", "project": "my-project" } }
  ],
  "count": 1
}
```

---

### `list_backends`

```json
{ "config_path": "" }
```

Output:

```json
{
  "backends": [
    { "kind": "gcp", "name": "prod-gcp", "hint": "my-project" },
    { "kind": "kubernetes", "name": "k8s-prod", "hint": "prod-context" }
  ]
}
```

---

### `exec_on_host`

Runs a command over SSH on a known IP or hostname. Typical pattern: call `search_hosts` first,
then pass `primary_ip` from a result to `exec_on_host`.

```json
{
  "host": "10.0.0.5",
  "name": "pg-primary",
  "command": "df -h /",
  "shell": "bash",
  "timeout_sec": 30
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `host` | **yes** | IP or hostname for SSH |
| `name` | no | Display label in output (defaults to `host`) |
| `command` | **yes** | Shell command to run |
| `shell` | no | Wrap in `bash -c` or `sh -c`; omit for direct exec |
| `timeout_sec` | no | 0–3600; 0 = no timeout |

Output:

```json
{
  "results": [
    { "host": "pg-primary", "ip": "10.0.0.5",
      "output": "Filesystem  Size  Used ...", "exit_code": 0, "error": "" }
  ]
}
```

---

## Notes

- **No token required** for stdio transport — the MCP session runs as the local user.
- **stdout is reserved** for the JSON-RPC stream; honey only writes to stderr.
- For the HTTP-based MCP endpoint (via `honey web`), see [Web UI](./web-ui.md).
