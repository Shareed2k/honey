---
id: plugins-development
title: Plugin development
slug: /plugins-development
---

Honey can load **WASM plugins** ([Extism](https://extism.org/)) to extend recipes and secrets without recompiling the CLI. Plugins are optional and **off by default**.

Typical uses:

- Rewrite CUE recipe bytes before compile (`cue_transform`)
- Custom per-host recipe steps (`plugin:` in CUE)
- Resolve custom secret ref prefixes (`myvault:…`)
- Local hooks after a step result (`hook`)
- Optional stack data-key unwrap (`stack_unwrap`)

**Reference implementation:** [`examples/plugins/echo/`](https://github.com/shareed2k/honey/tree/main/examples/plugins/echo) · **API types:** [`internal/plugins/api/v1`](https://github.com/shareed2k/honey/tree/main/internal/plugins/api/v1) · **Go helpers:** [`pkg/pluginpdk`](https://github.com/shareed2k/honey/tree/main/pkg/pluginpdk)

## Layout and enablement

Each plugin is a directory under the plugins root (default `~/.config/honey/plugins/<name>/`):

| File | Purpose |
|------|---------|
| `plugin.yaml` | Manifest: `id`, `capabilities`, permissions |
| `plugin.wasm` | Extism module (e.g. `GOOS=wasip1 GOARCH=wasm`) |

Enable in honey YAML:

```yaml
plugins:
  enabled: true
  directory: ""          # default ~/.config/honey/plugins
  allowlist: []          # optional plugin ids; empty = all discovered
  max_memory_mb: 32
  timeout_ms: 30000
  network_deny: false
  network_allow_hosts: []   # if set, each plugin's allowed_hosts must be a subset
```

List loaded plugins:

```bash
honey plugins list --config ~/.config/honey/config.yaml
```

Install the echo example:

```bash
make build-plugin-examples
mkdir -p ~/.config/honey/plugins/echo
cp examples/plugins/echo/plugin.yaml ~/.config/honey/plugins/echo/
cp examples/plugins/echo/plugin.wasm ~/.config/honey/plugins/echo/
```

## API version `honey.plugins/v1`

All WASM **exports** use JSON in → JSON out. On failure the host may see `{"error":"message"}` from Extism.

| Export | Manifest capability | Purpose |
|--------|---------------------|---------|
| `cue_transform` | `cue_transform` | Transform raw CUE bytes before compile |
| `execute_step` | `custom_step` | Run a recipe `plugin:` step per host |
| `resolve_secret` | `secret` | Resolve refs matching `secret_ref_prefixes` |
| `unwrap_stack_key` | `stack_unwrap` | Optional stack data-key unwrap |
| `on_step_result` | `hook` | Local hook after step result (command/script/plugin hooks) |

Set `"api_version": "honey.plugins/v1"` on every input struct.

### `cue_transform`

**Input:** `cue` is **base64** of the raw `.cue` file; `hosts_count` is the number of search rows (may be 0 at validate time).

**Output:** `cue` is base64 of the transformed bytes.

Plugins with `cue_transform` run in manifest **`order`** (lower first). Applied to `cue-validate`, `cue-exec`, TUI **r**, and web cue-exec.

### `execute_step`

**Input highlights:**

| Field | Meaning |
|-------|---------|
| `step_index` | 0-based step index in the recipe |
| `host` | JSON-encoded [`hosts.Record`](https://github.com/shareed2k/honey/blob/main/internal/hosts/record.go) |
| `env` | Effective env for this host (includes `HONEY_HOST_*`) |
| `plugin_id` | Manifest `id` |
| `action` | From recipe `plugin.action` |
| `config` | Optional JSON from recipe `plugin.config` |
| `execute` | `false` on dry-run |
| `secrets_dry_run` | Secrets redacted in env when true |

**Output:** `success`, `changed`, `skipped`, `exit_code`, `stdout`, `stderr`, `err` (Ansible-like module semantics when using remote host functions).

Recipe shape:

```cue
{
  host: "*"
  plugin: {
    id:     "echo"
    action: "noop"
    config: {}   // optional arbitrary JSON
  }
}
```

See [CUE Recipes](./cue-recipes.md) for `host` matching and graph mode.

### `resolve_secret`

**Input:** `ref` (full ref string), optional `label`, `plugin_id`.

**Output:** `value` (plaintext). Only refs whose prefix is listed in `secret_ref_prefixes` are routed to your plugin.

Production recipes should use `secure:v1:…` for real secrets; custom prefixes are for vault integrations or demos (`echo:` in the echo plugin).

### `on_step_result`

Used when a recipe hook uses `plugin:` instead of `command`. **Input** includes `phase` (`on_success` / `on_failure`), host JSON, and step result JSON. **Output:** `output` text appended to hook output.

## Manifest (`plugin.yaml`)

```yaml
id: myplugin
version: "0.1.0"
capabilities:
  - custom_step
  - secret
order: 10                    # cue_transform chain only
secret_ref_prefixes:
  - "myvault:"
allow_host_exec: false
allow_remote_exec: false
allow_sftp: false
allow_template_render: false
allow_postgres: false
allow_kv: false
allowed_env:
  - MY_API_TOKEN
allowed_hosts:
  - api.example.com
allowed_paths:
  "/data/inventory": "/var/lib/honey/inventory"
max_http_response_bytes: 1048576
```

| Field | Purpose |
|-------|---------|
| `order` | Sort order for `cue_transform` (lower runs first) |
| `secret_ref_prefixes` | Refs like `myvault:path` handled by `resolve_secret` |
| `allow_host_exec` | Grant `host_exec` host function (local argv on operator) |
| `allow_remote_exec` | Grant `remote_exec` (SSH/API shell on recipe target) |
| `allow_sftp` | Grant `remote_upload`, `remote_download`, `remote_stat` |
| `allow_template_render` | Grant `template_render` (Go text/template on operator) |
| `allow_postgres` | Grant `postgres_query`, `postgres_exec`, `postgres_migrate` (pgx on operator) |
| `allow_kv` | Grant `kv` host function (needs recipe `kv_tunnel: true`) |
| `allowed_env` | Env names readable via `get_env` |
| `allowed_hosts` | Hostnames for Extism HTTP from the plugin |
| `allowed_paths` | WASI filesystem map: guest path → host absolute path |
| `max_http_response_bytes` | Cap per HTTP response (default 4 MiB when memory limit set) |

### Network policy

- Empty or omitted `allowed_hosts` → **no outbound HTTP** from the plugin.
- Wildcards (`*`) are rejected in YAML.
- If honey config sets `plugins.network_allow_hosts`, every manifest hostname must be in that list.
- `plugins.network_deny: true` blocks all plugin HTTP even when the manifest lists hosts.

## Host functions

Available from WASM via Extism **user imports** (Go: `//go:wasmimport extism:host/user …`).

| Function | When available | Purpose |
|----------|----------------|---------|
| `log_info`, `log_warn` | Always | Structured logging on the operator host |
| `get_env` | `allowed_env` in manifest | Read allowlisted environment variables |
| `host_exec` | `allow_host_exec: true` | Argv-only subprocess on operator; **no shell** |
| `remote_exec` | `allow_remote_exec: true` | Run a script on the recipe target via honey SSH/SFTP/API shell |
| `remote_upload` | `allow_sftp: true` | SFTP put (local path or inline `content`) |
| `remote_download` | `allow_sftp: true` | SFTP get (size-capped) |
| `remote_stat` | `allow_sftp: true` | Remote path metadata |
| `template_render` | `allow_template_render: true` | Render Go text/template on operator (slim-sprig) |
| `postgres_query` | `allow_postgres: true` | Read-only SQL via pgx on operator (`$1` params) |
| `postgres_exec` | `allow_postgres: true` | INSERT/UPDATE/DDL via pgx (blocked on dry-run) |
| `postgres_migrate` | `allow_postgres: true` | Apply ordered `.sql` files from recipe dir |
| `kv` | `allow_kv: true` and recipe `kv_tunnel: true` | Get/put/delete in shared per-run stepkv |

### `host_exec`

**Input:**

```json
{"argv":["echo","ok"],"cwd":"","timeout_ms":5000}
```

**Output:**

```json
{"exit_code":0,"stdout":"ok","stderr":"","error":""}
```

Use for operator-side CLIs (`psql`, `curl`) when HTTP from WASM is awkward. **Security:** only enable when the plugin is trusted; argv-only reduces injection risk but the plugin still runs code on your laptop.

### `kv`

**Input:** `{"op":"get|put|delete","key":"my-key","value":"..."}`

**Output:** `{"found":true,"value":"…","error":""}`

Keys are shared with remote `command` / `script` steps that use `HONEY_KV_*` on the same `cue-exec` run. Parallel hosts may race on the same key — namespace with step id and host name (see [CUE Recipes — KV tunnel](./cue-recipes.md#recipe-kv-tunnel)).

### `remote_exec`

Runs a script on the **recipe target** (SSH, k8s exec, Proxmox, TrueNAS API shell, etc.). Honey core owns transport, retries, and dry-run — WASM never opens SSH.

**Input:**

```json
{"shell":"/bin/bash","script":"set -e\nhostname","run_as":"","timeout_ms":30000}
```

**Output:**

```json
{"exit_code":0,"stdout":"web1\n","stderr":"","changed":true,"failed":false,"error":""}
```

On dry-run (`execute: false`), the host returns `changed: true` and a plan string without connecting.

Use [`pkg/pluginpdk`](https://github.com/shareed2k/honey/tree/main/pkg/pluginpdk) helpers: `RemoteExec`, `RemoteUpload`, `RemoteStat`, `TemplateRender`, `PostgresQuery`, `PostgresExec`, `PostgresMigrate`.

### `postgres_query` / `postgres_exec`

Runs SQL on the **operator** via pgx. DSN is resolved in core from `config.dsn_secret` (recipe `secrets` key or direct `secure:v1:` ref). WASM never sees the DSN.

**Input:**

```json
{"dsn_secret":"PG_DSN","sql":"SELECT $1","params":["${THRESHOLD}"],"timeout_ms":10000,"readonly":true,"kv_key":"pg_activity","kv_key_per_host":true,"extract":{"count":".[0].n"}}
```

Optional **`kv_key`** stores full JSON stdout in recipe stepkv; **`extract`** runs jq (gojq on operator) and stores `{kv_key}_{name}` keys. Downstream steps use **`env_from.kv`**, template **`jqGet`**, or **`${VAR}`** in plugin config (expanded from merged env before pgx).

**Tunnel-aware DSN rewrite** — when Postgres runs on remote loopback, add a **`tunnel:`** step and reference it from the plugin config:

```cue
// Graph recipe excerpt
steps: [
  {
    id: "pg_tunnel"
    host: "db-*"
    tunnel: { remote_host: "localhost", remote_port: 5432 }
  },
  {
    id: "query"
    host: "db-*"
    depends: ["pg_tunnel"]
    plugin: {
      id: "postgres"
      action: "query"
      config: {
        dsn_secret: "PG_DSN"
        tunnel_step: "pg_tunnel"
        sql: "SELECT 1"
        params: []
      }
    }
  },
]
```

| Config field | Meaning |
|--------------|---------|
| `tunnel_step` | Step `id` of a **`tunnel`** step in the same run; rewrites DSN host/port to the operator listen address |
| `host` / `port` | Optional overrides after `tunnel_step` (supports `${VAR}` expansion) |

Precedence: resolve DSN from `dsn_secret` → apply **`tunnel_step`** endpoint → apply explicit **`host`** / **`port`**. TCP tunnels only.

Examples: [`postgres_tunnel_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/postgres_tunnel_demo.cue), [`postgres_module_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/postgres_module_demo.cue) (no tunnel). General tunnel usage: [CUE Recipes — Tunnel steps](./cue-recipes.md#tunnel-steps).

**Output:**

```json
{"changed":true,"rows":[{"n":1}],"stdout":"[{\"n\":1}]"}
```

Safety: `timeout_ms` required; `readonly` defaults to `true`; dry-run returns a plan without connecting; SQL text is audit-logged (SHA256 + truncated preview, never params/DSN).

### Rclone RC API (`rclone` plugin)

Calls **rclone rcd** over HTTP from the operator via a recipe **`tunnel:`** step (SSH local forward to remote `127.0.0.1:5572`). The plugin does **not** start rcd — use a prior **`command`** step or systemd on the target.

Enable HTTP to loopback:

```yaml
plugins:
  enabled: true
  network_allow_hosts:
    - "127.0.0.1"
```

Recipe pattern (see [`rclone_rc_tunnel.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/rclone_rc_tunnel.cue)):

```cue
steps: [
  {
    id: "rcd_ensure"
    host: "role:app"
    command: "systemctl is-active --quiet rclone-rcd || systemctl start rclone-rcd"
  },
  {
    id: "rcd_tunnel"
    host: "role:app"
    depends: ["rcd_ensure"]
    tunnel: { remote_host: "127.0.0.1", remote_port: 5572 }
  },
  {
    id: "rc_copy"
    host: "role:app"
    depends: ["rcd_tunnel"]
    plugin: {
      id: "rclone"
      action: "copy"
      config: {
        tunnel_step: "rcd_tunnel"
        rc_user: "honey"
        rc_pass: "${RCD_PASS}"
        params: { srcFs: "s3:bucket", dstFs: "local:/data" }
      }
    }
  },
]
```

| Config field | Meaning |
|--------------|---------|
| `tunnel_step` | **Required on execute.** Step `id` of a **`tunnel`** step; host rewrites `base_url` to `http://127.0.0.1:<local_port>` |
| `base_url` | Optional override after `tunnel_step` |
| `rc_user` / `rc_pass` | Basic auth for rcd (`rc_pass` supports `${VAR}` from recipe secrets) |
| `params` | JSON body for the RC endpoint (action-specific) |

**Actions (v1):** `noop`, `copy`, `sync`, `list`, plus `about`, `move`, `delete`, `mkdir`, `job_status`, `job_finish`, `mount`, `unmount`, `vfs_refresh`, `vfs_stats`.

**Secrets:** `secret_ref_prefixes: ["rclone:"]` resolves `rclone:rcd` from operator env `RCLONE_RCD` (see manifest `allowed_env`).

Dry-run: when the tunnel is active, POST `core/noop` to verify rcd; otherwise reports a plan line without connecting.

### Built-in WASM modules

Shipped under [`plugins/`](https://github.com/shareed2k/honey/tree/main/plugins) (Ansible-like wrappers):

| Plugin | Action | Purpose |
|--------|--------|---------|
| `bash` | `run` | Remote `/bin/bash` script |
| `shell` | `run` | Remote `/bin/sh` script |
| `copy` | `put` | SFTP upload local → remote |
| `template` | `put` | Render template + upload |
| `file` | `manage` | `directory` / `absent` / `touch` |
| `service` | `manage` | `systemctl` started/stopped/restarted |
| `postgres` | `query` / `exec` / `migrate` | Host-mediated pgx (DSN from recipe secrets) |
| `rclone` | `noop` / `copy` / `sync` / `list` / … | rclone RC HTTP via tunneled `rcd` on remote loopback |

Build and install:

```bash
make build-plugin-modules
mkdir -p ~/.config/honey/plugins/bash
cp examples/plugins/bash/plugin.yaml examples/plugins/bash/plugin.wasm ~/.config/honey/plugins/bash/
```

Example recipes: [`bash_module_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/bash_module_demo.cue), [`postgres_module_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/postgres_module_demo.cue), [`postgres_kv_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/postgres_kv_demo.cue), [`rclone_rc_tunnel.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/rclone_rc_tunnel.cue), [`tunnel_local_forward.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_local_forward.cue).

For simple one-off shell, prefer native `command` / `script` steps (no WASM). Use modules when you want structured `changed` / validation / composable actions.

### HTTP

Use the [Extism Go PDK HTTP API](https://github.com/extism/go-pdk) (`pdk.NewHTTPRequest` / `Send`) against hosts in `allowed_hosts`. Other [Extism PDKs](https://extism.org/docs) work for non-Go languages.

## Authoring in Go

### Project layout

```
myplugin/
  go.mod
  main.go
  plugin.yaml
  plugin.wasm   # build output
```

**`go.mod`** (path to honey repo):

```go
module example.com/myplugin

go 1.26

require (
  github.com/extism/go-pdk v1.1.3
  github.com/shareed2k/honey v0.0.0
)

replace github.com/shareed2k/honey => /path/to/honey
```

### Build WASM

From the plugin directory:

```bash
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
```

From the honey repo root (echo example):

```bash
make build-plugin-examples
```

Built-in Ansible-like modules (`bash`, `shell`, `copy`, `template`, `file`, `service`, `rclone`):

```bash
make build-plugin-modules
```

This also copies the echo wasm into `internal/plugins/testdata/echo/` for CI.

### Exports (Go PDK)

```go
//go:wasmexport execute_step
func executeStep() int32 {
  var in executeStepInput
  if err := pdk.InputJSON(&in); err != nil {
    pdk.SetError(err)
    return 1
  }
  // ...
  return pdk.OutputJSON(executeStepOutput{Success: true, Stdout: "ok"})
}
```

Match JSON field names to [`internal/plugins/api/v1`](https://github.com/shareed2k/honey/blob/main/internal/plugins/api/v1/api.go).

### Recipe KV from Go (`pkg/pluginpdk`)

1. **`plugin.yaml`:** `allow_kv: true`
2. **Recipe:** `defaults: { kv_tunnel: true }` or per-step `kv_tunnel: true`
3. **Code:**

```go
import "github.com/shareed2k/honey/pkg/pluginpdk"

if err := pluginpdk.KVPut("my-key", "value"); err != nil { /* handle */ }
val, found, err := pluginpdk.KVGet("my-key")
if err := pluginpdk.KVDelete("my-key"); err != nil { /* handle */ }
```

Demo: [`echo_plugin_kv_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/echo_plugin_kv_demo.cue).

Non-Go PDKs: call the `kv` host function with the same JSON shape.

## Capability cheat sheet

| You want to… | Enable in manifest | Export / recipe |
|--------------|-------------------|-----------------|
| Call REST/GraphQL | `allowed_hosts` + PDK HTTP | — |
| Read API token from env | `allowed_env` | `get_env` |
| Read files / SQLite on disk | `allowed_paths` | WASI paths |
| Run CLI on operator machine | `allow_host_exec: true` | `host_exec` in plugin code |
| Share scratch state with remote steps | `allow_kv: true` | recipe `kv_tunnel: true` |
| Custom secret refs | `secret` + `secret_ref_prefixes` | `resolve_secret` |
| Rewrite recipes before compile | `cue_transform` | `cue_transform` |
| Per-host custom step | `custom_step` | recipe `plugin: { id, action }` |
| Local hook after step | `hook` | `on_step_result` + recipe hook `plugin:` |

Postgres/MySQL **client libraries inside WASM** are usually a poor fit (TCP + drivers in the guest). Prefer **HTTP APIs**, **`host_exec` + CLI**, or operator-side integration.

## Echo plugin walkthrough

The **echo** plugin is the minimal reference:

| Feature | Behavior |
|---------|----------|
| `cue_transform` | Prepends `// honey-echo-transform\n` to CUE bytes |
| `execute_step` + dry-run | `stdout: "dry-run"` |
| `action: "noop"` + execute | `stdout: "executed"` |
| `action: "host_exec"` | Runs `echo ok` via `host_exec` |
| `action: "kv_ping"` + `kv_tunnel` | Put/get `echo-kv-ping` via `pluginpdk` |
| `resolve_secret` | `echo:VALUE` → plaintext `VALUE` |

```bash
# Dry-run
honey cue-exec examples/recipe/echo_plugin_demo.cue <search-filter>

# Execute
honey cue-exec --execute examples/recipe/echo_plugin_demo.cue <search-filter>
```

Full install steps: [echo/README.md](https://github.com/shareed2k/honey/blob/main/examples/plugins/echo/README.md) in the repo.

## Hooks with plugins

Recipe hooks (`hooks.on_success` / `on_failure`) on **command**, **script**, or **plugin** steps may use `plugin:` instead of `command` when `where: "local"`:

```cue
hooks: {
  on_success: {
    where: "local"
    plugin: {
      id:     "myplugin"
      action: "notify"
    }
  }
}
```

The host calls `on_step_result` on the plugin with `phase`, host, and result payload.

## Security

- Plugins run on the **operator machine** with configurable memory, timeout, and network allowlists.
- **`host_exec`** runs arbitrary argv on the operator — only install trusted plugins.
- **`allowed_paths`** maps host filesystem into the guest; keep paths minimal.
- Secret material from `resolve_secret` flows into recipe env like any other secret backend.
- Use `plugins.allowlist` in production to load only known plugin ids.

## Related

- [CUE Recipes](./cue-recipes.md) — `plugin:` steps, `tunnel:` steps, `kv_tunnel`, graph mode
- [CLI: honey cue-exec](./cli/honey_cue-exec.md)
- Repo: [`examples/plugins/README.md`](https://github.com/shareed2k/honey/blob/main/examples/plugins/README.md)
