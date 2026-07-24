---
id: plugins-development
title: Plugin development
slug: /plugins-development
---

Honey can load plugins to extend recipes and secrets without recompiling the CLI, in two runtimes. Plugins are optional and **off by default**.

- **`runtime: wasm`** (default) — a sandboxed [Extism](https://extism.org/) module. Covered in most of this page.
- **`runtime: docker`** — execs a real binary (`mongosh`, `aws`, `gcloud`, `duckdb`, `ffmpeg`, …) inside a long-lived container. No WASM, no build step. See [Docker runtime plugins](#docker-runtime-plugins) below.

Typical WASM uses:

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
task build-plugin-examples
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

### `kv_key` / `kv_key_per_host`

Any plugin step (WASM or `runtime: docker`) can capture its raw stdout into the recipe KV store — the escape hatch for output too large for `env_from`'s 8192-byte cap (the KV store caps at 65536 bytes, rejecting rather than truncating an oversized value):

```cue
plugin: {
  id:     "stealth_browser"
  action: "fetch"
  config: {url: "https://example.com"}
  kv_key:         "stealth_fetch"  // recipe KV key
  kv_key_per_host: false            // true suffixes the key with a sanitized host name
}
```

| Field | Default | Meaning |
|-------|---------|---------|
| `kv_key` | _none_ | KV key the action's raw stdout is written to after a successful call. Omit to skip KV entirely (the common case). |
| `kv_key_per_host` | `false` | Suffix the key with the target host's (sanitized) name, so parallel hosts don't overwrite each other's value |

Written **only on a real `--execute` run** (dry-run never touches KV, consistent with plugins never really executing on dry-run) — right after the plugin call returns successfully, using its raw stdout, before it's combined with stderr for the step's own output. A downstream step reads it back with `{{ kvGet "stealth_fetch" }}` inside a [templated command/script step](./cue-recipes.md#templated-commandscript-steps), or the plain `kvGet`/`kvHas` template functions inside a `template:` step.

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

### SQLite (`sqlite` plugin)

Runs SQLite inside the WASM plugin using the embedded `github.com/ncruces/go-sqlite3` driver. It does **not** call a `sqlite3` binary, does **not** use `host_exec`, and does **not** add a Honey host-side SQLite function. Database files are visible only through WASI `allowed_paths` mounts in `plugin.yaml`.

Build note: the plugin must be built for WASI with the `sqlite3_dotlk` tag:

```bash
GOOS=wasip1 GOARCH=wasm go build -tags sqlite3_dotlk -buildmode=c-shared -o plugin.wasm .
```

Manifest path mount example:

```yaml
id: sqlite
version: "0.1.0"
capabilities:
  - custom_step
allow_kv: true
allowed_paths:
  "/sqlite": "/var/lib/honey/sqlite"
```

Recipe query example:

```cue
plugin: {
  id:     "sqlite"
  action: "query"
  config: {
    dsn:      "file:/sqlite/app.db?mode=ro"
    readonly: true
    sql:      "SELECT id, name FROM users WHERE active = ?"
    params:   [true]
  }
}
```

Recipe exec example:

```cue
plugin: {
  id:     "sqlite"
  action: "exec"
  config: {
    dsn:    "file:/sqlite/app.db?mode=rw"
    sql:    "INSERT INTO audit(event) VALUES (?)"
    params: ["checked"]
  }
}
```

| Config field | Meaning |
|--------------|---------|
| `dsn` | SQLite filename or URI. Use a WASI guest path such as `file:/sqlite/app.db?mode=ro`. |
| `sql` | SQL statement. Use `?` placeholders for parameters. |
| `params` | Positional bind parameters passed to SQLite. |
| `readonly` | When `true`, `exec` is refused. Prefer `mode=ro` in query DSNs. |
| `timeout_ms` | Optional per-operation timeout. Defaults to 30000 ms. |
| `kv_key` / `kv_key_per_host` | Optional storage of JSON stdout in recipe stepkv. |

WASI file locking uses `sqlite3_dotlk`; avoid concurrent writes from other SQLite implementations against the same database file.

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
| `sqlite` | `query` / `exec` | Embedded SQLite in WASM against `allowed_paths` DB files |
| `rclone` | `noop` / `copy` / `sync` / `list` / … | rclone RC HTTP via tunneled `rcd` on remote loopback |

Build and install:

```bash
task build-plugin-modules
mkdir -p ~/.config/honey/plugins/bash
cp examples/plugins/bash/plugin.yaml examples/plugins/bash/plugin.wasm ~/.config/honey/plugins/bash/
```

Example recipes: [`bash_module_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/bash_module_demo.cue), [`postgres_module_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/postgres_module_demo.cue), [`postgres_kv_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/postgres_kv_demo.cue), [`sqlite_module_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/sqlite_module_demo.cue), [`rclone_rc_tunnel.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/rclone_rc_tunnel.cue), [`tunnel_local_forward.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_local_forward.cue).

For simple one-off shell, prefer native `command` / `script` steps (no WASM). Use modules when you want structured `changed` / validation / composable actions.

### HTTP

Use the [Extism Go PDK HTTP API](https://github.com/extism/go-pdk) (`pdk.NewHTTPRequest` / `Send`) against hosts in `allowed_hosts`. Other [Extism PDKs](https://extism.org/docs) work for non-Go languages.

## Docker runtime plugins

For tools better run as their real CLI/binary than reimplemented in WASM (`mongosh`, `aws`, `gcloud`, `duckdb`, `ffmpeg`, `psql`, …), set `runtime: docker` instead of shipping a `.wasm` module. No Go/WASM build step — a plugin is just `plugin.yaml` + `plugin.cue`.

### Layout

| File | Purpose |
|------|---------|
| `plugin.yaml` | Manifest: `id`, `capabilities`, `runtime: docker`, `docker: {...}` |
| `plugin.cue` | Declares each action's `argv`, `#Config` schema, `output_format`, optional `env`/`stdin` |

### Manifest fields (`docker:`)

```yaml
id: mongodb
version: "0.1.0"
capabilities:
  - custom_step
runtime: docker
docker:
  image: "mongo:latest"
  pull_policy: if_not_present   # if_not_present (default) | always
  restart:
    max_backoff: 30s            # default 30s; unbounded retries, capped interval
  volumes:
    - "/var/honey/data:/data:rw"  # host_path:container_path[:ro|rw]
```

| Field | Default | Purpose |
|-------|---------|---------|
| `docker.image` | _required_ | Image to run; pulled per `pull_policy` |
| `docker.pull_policy` | `if_not_present` | `if_not_present` or `always` |
| `docker.restart.max_backoff` | `30s` | Cap on exponential backoff between restart attempts after a crash — retries are unbounded, never permanently give up |
| `docker.volumes` | _none_ | Static bind mounts (Docker `Binds` syntax); use for actions that read/write files across calls (e.g. `ffmpeg` writing then `ffprobe` reading the same file) |
| `allowed_env` | _none_ | Same manifest field as WASM plugins, but for docker plugins these are passed straight into the container's environment at creation time — not gated behind a per-call host function |

The container is created once when the plugin loads and stays up for the plugin's lifetime (like a WASM module instance) — not one container per call. If it crashes, honey restarts it automatically with exponential backoff (capped at `max_backoff`, retried forever).

### `plugin.cue`

One `actions: <name>: {...}` block per action:

```cue
actions: query: {
	#Config: {
		uri:        string
		database:   string
		collection: string
		query:      string
	}

	argv: [
		"mongosh",
		config.uri,
		"--quiet",
		"--eval",
		"EJSON.stringify(db.getSiblingDB('\(config.database)').getCollection('\(config.collection)').find(\(config.query)).toArray())"
	]

	output_format: "json"
}
```

| Field | Required | Purpose |
|-------|----------|---------|
| `#Config` | recommended | CUE schema the recipe's `plugin.config` is validated against before every call |
| `argv` | yes | Argv exec'd directly inside the container — **not** run through the image's own `ENTRYPOINT`/shell (see gotcha below) |
| `output_format` | no (default `"text"`) | `"json"` decodes stdout as JSON for the step's captured output; `"text"` keeps it as a string |
| `env` | no | Extra env vars for just this call, evaluated from `config` like `argv` — use for secrets (e.g. a resolved DB password) that shouldn't appear in `argv`/`ps`/`/proc/<pid>/cmdline` |
| `stdin` | no | Text piped to the process's stdin, evaluated from `config` — use for request bodies (e.g. a JSON query DSL) that are awkward to shell-quote into `argv` |

**Gotcha:** honey always overrides the container's `Entrypoint` with the `honey-plugin-init` shim, which execs `argv[0]` directly. If an image's default entrypoint does argument-wrapping (e.g. some `ffmpeg` images wrap `ffmpeg`/`ffprobe` in a shell script), use the binary's absolute path in `argv[0]` (check with `docker inspect <image>`) instead of relying on the image's own entrypoint behavior.

### Packaging: `honey-plugin-init`

Docker-runtime plugins bind-mount a small shim binary, `honey-plugin-init`, as the container's entrypoint (it execs `argv` and returns `{output, stderr, exit_code, error}` over a loopback HTTP call). It must exist **alongside the `honey` executable** — build it with:

```bash
task build-honey-plugin-init
```

Or point at a prebuilt binary with the `HONEY_PLUGIN_INIT_PATH` environment variable. Without one of these, loading a `runtime: docker` plugin fails with `honey-plugin-init not found at ... (build it via task build-honey-plugin-init or set HONEY_PLUGIN_INIT_PATH)`.

### Embedded-init (registry-distributed) plugins

Bind mode (above) requires the **operator's** machine to have `honey-plugin-init` built or available locally — fine for local dev, awkward for an image you publish for other people to `docker pull` and run as-is. For that case, set `docker.init: embedded` in the manifest: the image itself already carries `honey-plugin-init` as its entrypoint, so honey doesn't bind-mount or override anything — it just starts the container and talks to the shim already baked in.

```yaml
runtime: docker
docker:
  image: "ghcr.io/you/my-plugin@sha256:<digest>"
  init: embedded          # image supplies honey-plugin-init; no host binary needed
  init_path: /usr/local/bin/honey-plugin-init   # optional; this is the default
```

`docker.init: embedded` requires `docker.image` to be **digest-pinned** (`...@sha256:...`, not just a tag) — honey rejects the manifest at load otherwise. Only a digest guarantees every host pulls the exact bytes that were built and verified; a mutable tag can point at different content per architecture or be repointed later.

Build the image with `honey-plugin-init` at `/usr/local/bin/honey-plugin-init` (or wherever `docker.init_path` says) using one of two patterns:

**Pattern A — `COPY --from` the published base image** (recommended; the shim ships prebuilt, so there's nothing to compile):

```dockerfile
# syntax=docker/dockerfile:1
FROM your-base-image
COPY --from=ghcr.io/shareed2k/honey-plugin-init:<ver> \
    /usr/local/bin/honey-plugin-init /usr/local/bin/honey-plugin-init
ENTRYPOINT ["/usr/local/bin/honey-plugin-init"]
```

**Pattern B — `ADD` the release binary directly** (no dependency on the base image):

```dockerfile
# syntax=docker/dockerfile:1
FROM your-base-image
ARG TARGETARCH
ADD https://github.com/shareed2k/honey/releases/download/v<ver>/honey-plugin-init-linux-${TARGETARCH} /usr/local/bin/honey-plugin-init
RUN chmod 0755 /usr/local/bin/honey-plugin-init
ENTRYPOINT ["/usr/local/bin/honey-plugin-init"]
```

Either way, **the final image must be a multi-arch manifest list** — build and push it with `docker buildx build --platform linux/amd64,linux/arm64 ... --push` covering every architecture your fleet runs. A single-arch image works fine on a matching host but fails at container start with an `exec format error` on a mismatched one (e.g. an amd64-only image pulled on an arm64 host). `docker manifest inspect <image>@sha256:...` shows whether a reference is a manifest list or a single-platform image before you ship it.

### Host networking (`docker.network: host`)

By default a docker-runtime plugin's container runs on the normal Docker bridge network, isolated from the daemon host's own network stack. Set `docker.network: host` in the manifest to instead run the container with the daemon host's network namespace — the container sees (and can reach) exactly what the host itself can, including the host's own loopback interface:

```yaml
runtime: docker
docker:
  image: "pghero_diagnostics:latest"
  network: host   # "" (default, bridge) | "host"
```

Host networking is **operator-gated**, off by default: the operator must opt in with `plugins.allow_host_network: true` in `honey.yaml`, separately from whatever the plugin's own manifest requests. A plugin manifest alone cannot turn this on — a plugin author asking for `network: host` is not the same as an operator granting it. If a loaded plugin requests `network: host` while the toggle is off, honey refuses to load it (error mentions `allow_host_network`).

```yaml
plugins:
  allow_host_network: true   # required, or a docker.network: host plugin fails to load
```

**Why gated:** host networking is a privilege escalation relative to the normal container network sandbox — it hands the container the daemon host's full network namespace (every interface, every port the host can reach), not just an isolated bridge segment. Treat `plugins.allow_host_network: true` the same as any other operator-side widening of a plugin's trust boundary (alongside `docker.image`/`allowed_env`, see [Security](#security)).

Even in host mode the shim itself only ever binds **loopback**: honey allocates a free port on `127.0.0.1` and tells the shim to bind that address directly (no port publishing — host networking has no published-ports concept to begin with). The container's other host-network privileges (reaching the host's other interfaces, other services bound to the host) are a side effect of the network mode, not something the shim itself uses.

This is primarily a **Linux** feature. Docker Desktop and Colima run the daemon inside a VM, so their own "host" network mode is still scoped to that VM, not the real host — and both already expose `host.docker.internal` as a bridge-reachable gateway to the operator's loopback, which is simpler and works today (see the [Manifest fields](#manifest-fields-docker) example and [`pghero_tunnel_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/pghero_tunnel_demo.cue)). On plain Linux there is no such gateway and no bridge route to the operator's loopback at all, so `network: host` is the way to reach it: e.g. an operator-side recipe `tunnel:` step opens a local SSH port-forward on `127.0.0.1:15432`, and a host-networked plugin container reaches that same `127.0.0.1:15432` directly, because it shares the operator's loopback.

### Running a docker plugin on a remote host

By default a `runtime: docker` plugin's container runs on the **operator's** Docker daemon. A recipe step's `host:` decides where instead:

- `host: "_"` (or `localhost`/`127.0.0.1`) — the operator's local daemon (the default; unchanged).
- any real matched host — on **that host's** Docker daemon, over the SSH connection honey already uses for that host.

When a step targets a real host on `--execute`, honey:

1. tunnels the Docker Engine API to the host over SSH (same mechanism as the `docker:` step),
2. stages the arch-matched `honey-plugin-init` shim to `/tmp/honey-plugin-init-linux-<arch>` on the host (uploaded once, checksum-skipped on later runs — nothing else is installed on the host),
3. runs the plugin's shim-container on the host's daemon and reaches the shim through the same SSH connection.

Because the container is created by the **remote** daemon, `docker.volumes` bind mounts resolve on the remote host — e.g. `"/var/run/docker.sock:/var/run/docker.sock"` mounts the *host's* Docker socket, letting the plugin manage the host's own containers. The container is scoped to the run and stopped+removed when the recipe finishes.

Requirements: the host needs SSH (already used by honey) and a Docker daemon — nothing else. The **operator** needs the shim binary matching the *remote host's* architecture — `honey-plugin-init-linux-<arch>` — available in one of: `$HONEY_PLUGIN_INIT_DIR`, the directory of `$HONEY_PLUGIN_INIT_PATH` (its arch-suffixed siblings), or alongside the `honey` binary (where releases ship both arches). In dev, `task build-honey-plugin-init` writes both arches to `build/`; point `HONEY_PLUGIN_INIT_DIR` there. WASM plugins are unaffected — they always run in-process on the operator.

Dry-run keeps everything local (no remote containers), and cross-run container reuse is not done (one container per host per run).

**Proxmox VMs/LXCs**: LXC guests always work, regardless of the backend's `exec_mode` — Proxmox has no LXC exec REST endpoint, so LXC command execution is always SSH-backed. QEMU VMs work when the backend's `exec_mode` is `ssh` (the default) or `hybrid` (which runs commands through the QEMU guest agent but still keeps a real SSH connection open for file transfers — reused here for the docker tunnel). `exec_mode: pve` (pure QEMU guest-agent, no SSH at all) cannot run a docker plugin remotely — there is no SSH connection to tunnel through.

**When the host has no reachable SSH at all** (inbound firewalled/CGNAT, port 22 refused): the operator can't reach *into* it for the tunnel. Instead, run honey **on that host** with `mesh.enabled: true` — it dials *out* to a relay and becomes reachable over the libp2p mesh — and run the docker plugin **locally on that host** (`host: "_"`, its own Docker daemon), while the operator reaches and manages it over the relay via a `backends.honey` entry with `mesh: true`. No inbound port, no SSH into the host, no remote tunnel. See [`examples/mesh`](https://github.com/shareed2k/honey/tree/main/examples/mesh) → "Reaching a firewalled host that runs Docker".

### Known limitation: dry-run

Unlike WASM plugins, `runtime: docker` actions currently always execute — `execute: false` (dry-run) and secrets-dry-run are not yet threaded through to the container call. Don't rely on dry-run to preview a docker-runtime plugin action's side effects; test against a disposable target first.

### Examples

[`examples/plugins/`](https://github.com/shareed2k/honey/tree/main/examples/plugins) ships several ready-to-copy docker-runtime plugins — no build step, just copy `plugin.yaml` + `plugin.cue` into your plugins directory:

| Plugin | Image | Actions |
|--------|-------|---------|
| [`mongodb/`](https://github.com/shareed2k/honey/tree/main/examples/plugins/mongodb) | `mongo:latest` | `query`, `eval` |
| [`duckdb/`](https://github.com/shareed2k/honey/tree/main/examples/plugins/duckdb) | `duckdb/duckdb:latest` | `query`, `export_parquet` |
| [`aws/`](https://github.com/shareed2k/honey/tree/main/examples/plugins/aws) | `amazon/aws-cli:latest` | `s3_ls`, `s3_cp`, `s3_rm`, `ec2_describe`, `ec2_start`, `ec2_stop` |
| [`gcloud/`](https://github.com/shareed2k/honey/tree/main/examples/plugins/gcloud) | `gcr.io/google.com/cloudsdktool/cloud-sdk:slim` | `compute_list`, `compute_start`, `compute_stop`, `storage_ls`, `storage_cp`, `storage_rm` |
| [`watchtower/`](https://github.com/shareed2k/honey/tree/main/examples/plugins/watchtower) | `docker.io/beatkind/watchtower:latest` | `check` (monitor-only, text output), `check_json` (same, single JSON document via watchtower's built-in `json.v1` notification template — see plugin.cue for the exact shape), `update` — mounts the daemon's Docker socket; pair with `host: "prod-*"` to check each server's own images (see [`watchtower_image_check.cue`](https://github.com/shareed2k/honey/tree/main/examples/recipe/watchtower_image_check.cue)). |

`gcr.io/google.com/cloudsdktool/cloud-sdk:slim` is **amd64-only** (no arm64 manifest) — on an Apple Silicon host with a VM-backed Docker daemon (Colima, Docker Desktop) with no qemu emulation registered, it fails with `exec format error`. The other three images are multi-arch.

```bash
mkdir -p ~/.config/honey/plugins/mongodb
cp examples/plugins/mongodb/plugin.yaml examples/plugins/mongodb/plugin.cue ~/.config/honey/plugins/mongodb/
```

Recipe usage is identical to WASM plugins — `plugin: { id, action, config }`:

```cue
{
  host: "*"
  plugin: {
    id:     "mongodb"
    action: "query"
    config: {
      uri:        "mongodb://db.internal:27017"
      database:   "app"
      collection: "users"
      query:      "{}"
    }
  }
}
```

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
task build-plugin-examples
```

Built-in Ansible-like modules (`bash`, `shell`, `copy`, `template`, `file`, `service`, `rclone`):

```bash
task build-plugin-modules
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
- **`runtime: docker`**: the container image is arbitrary — pin a digest or a trusted registry, since honey runs whatever `docker.image` says with no sandboxing beyond normal container isolation. `allowed_env` values are passed straight into the container at creation (unlike WASM's per-call gated `get_env`), so treat `docker.image` + `allowed_env` together as the plugin's full trust boundary.
- **`docker.network: host`**: widens that trust boundary further — the container gets the daemon host's full network namespace instead of an isolated bridge segment. Off by default; requires the operator to opt in with `plugins.allow_host_network: true`, independent of the plugin's own manifest. See [Host networking](#host-networking-dockernetwork-host).

## Related

- [CUE Recipes](./cue-recipes.md) — `plugin:` steps, `tunnel:` steps, `kv_tunnel`, graph mode
- [CLI: honey cue-exec](./cli/honey_cue-exec.md)
- Repo: [`examples/plugins/README.md`](https://github.com/shareed2k/honey/blob/main/examples/plugins/README.md)
