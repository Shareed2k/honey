---
id: mcp-server
title: MCP Server
---

`honey mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io/) server over **stdin/stdout** using the official [`go-sdk`](https://github.com/modelcontextprotocol/go-sdk). This lets AI coding assistants (Cursor, LM Studio, OpenCode, and others) search your infrastructure and list backends as MCP tools.

## Tools

| Tool | Purpose |
|------|---------|
| `search_hosts` | Same parallel search as `honey search`; arguments mirror CLI flags (snake_case JSON). Optional `config_path`; otherwise uses `HONEY_CONFIG` / default paths. |
| `list_backends` | Returns configured backends from YAML (`kind`, `name`, `hint`). Requires a resolvable config file. |

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

## Notes

- **No token required** for stdio transport — the MCP session runs as the local user.
- **stdout is reserved** for the JSON-RPC stream; honey only writes to stderr.
- For the HTTP-based MCP endpoint (via `honey web`), see [Web UI](./web-ui.md).
