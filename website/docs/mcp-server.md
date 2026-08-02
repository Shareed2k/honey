---
id: mcp-server
title: MCP Server
---

`honey mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io/) server over **stdin/stdout** using the official [`go-sdk`](https://github.com/modelcontextprotocol/go-sdk). This lets AI coding assistants (Cursor, LM Studio, OpenCode, and others) search your infrastructure and list backends as MCP tools.

## Tools

| Tool | Risk | Purpose |
|------|------|---------|
| `search_hosts` | read-only | Same parallel search as `honey search`; input fields mirror CLI flags (snake_case JSON). Optional `overrides` map for per-request provider settings. |
| `list_backends` | read-only | Returns configured backends from YAML (`kind`, `name`, `hint`). Requires a resolvable config file. |
| `get_host_details` | read-only | Resolve one host by name (optionally scoped to a `provider`) and return its full `Record` plus derived capabilities. |
| `plan_command` | read-only | Run a command through the command-risk engine + OPA policy **without executing it** — no SSH dial, no state change. Useful for an agent to check "would this be allowed?" before calling `exec_on_host`. |
| `exec_on_host` | **destructive** | Run a shell command on a host via SSH. Gated by the command-risk engine + OPA policy (see below). Use `primary_ip` from a `search_hosts` result. |

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
  "ssh_user": "",
  "cache_ttl": "",
  "cache_dir": "",
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

### `get_host_details`

```json
{ "name": "pg-primary", "provider": "gcp" }
```

`provider` is optional; when omitted, the first matching record across all backends is returned.

Output:

```json
{
  "record": { "name": "pg-primary", "primary_ip": "10.0.0.5", "provider": "gcp", "meta": {} },
  "capabilities": ["ssh", "docker_exec"]
}
```

---

### `plan_command`

Evaluates a command through the command-risk engine + OPA policy **without connecting to anything** — safe to call speculatively before `exec_on_host`.

```json
{ "command": "rm -rf /var/lib/postgresql/old-backup", "target": "pg-primary", "interpreter": "" }
```

Output:

```json
{
  "decision": "deny",
  "risk": "critical",
  "signals": [{ "id": "...", "severity": "critical", "reason": "..." }],
  "reason": "command risk: ..."
}
```

`decision` is `"allow"` or `"deny"`.

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

#### Command gating (secure by default)

`exec_on_host` runs every command through the same gate as the CLI/web/recipe
paths before any SSH connection:

- **Command-risk engine** (always on): deterministic static analysis denies
  critical commands — `mkfs`, `dd` to a block device, recursive `chmod`/`chown`
  of system paths, `curl | sh`, fork bombs. This holds even with no policy
  configured, so an AI agent cannot drive a destructive command to a host. The
  LLM is advisory-only and cannot override a deny.
- **Deny-by-default with no policy configured:** unlike the CLI/web/recipe
  paths, `exec_on_host` denies **every** command — not just critical ones —
  when no OPA enforcer is wired in. Either set `HONEY_POLICY_DIR` to a
  directory of `.rego` files, or set `HONEY_EXEC_ALLOW_UNVERIFIED=1` to allow
  execution without a policy (only past the command-risk check above — not
  recommended for AI clients).
- **OPA policy** (opt-in): set `HONEY_POLICY_DIR` to a directory of `.rego`
  files. The MCP path evaluates the `mcp_exec` action; a `deny`,
  `require_approval`, or `require_biometric` verdict refuses the call (there is
  no interactive approval over stdio MCP — the command is blocked with the
  reason).

A blocked call returns an error result like `blocked: command risk: filesystem
creation destroys existing data` and performs no SSH. The escape hatch
`HONEY_RISK_DISABLE=1` bypasses the gate for trusted automation (not
recommended for AI clients).

---

## Notes

- **No token required** for stdio transport — the MCP session runs as the local user.
- **stdout is reserved** for the JSON-RPC stream; honey only writes to stderr.
- `honey mcp` is stdio-only today; there is no separate HTTP-based MCP endpoint.
