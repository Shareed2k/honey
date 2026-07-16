# honey plugins (WASM + Docker runtime)

Plugins live under `~/.config/honey/plugins/<name>/`. Two runtimes:

- **`runtime: wasm`** (default) — sandboxed [Extism](https://extism.org/) module.
  - `plugin.yaml` — manifest (id, capabilities, optional `secret_ref_prefixes`)
  - `plugin.wasm` — Extism module, built with the [Extism Go PDK](https://github.com/extism/go-pdk) or other [Extism PDKs](https://extism.org/docs)
- **`runtime: docker`** — runs a real binary (`mongosh`, `aws`, `gcloud`, `duckdb`, `ffmpeg`, …) inside a container. No WASM module and **no build step** — just two text files:
  - `plugin.yaml` — manifest with `runtime: docker` and `docker: {image: "..."}`
  - `plugin.cue` — declares each action's argv/output shape (see [Docker runtime plugins](../../website/docs/plugins-development.md#docker-runtime-plugins))

See [Docker runtime plugins](../../website/docs/plugins-development.md#docker-runtime-plugins) for the full manifest/`plugin.cue` schema, and [`mongodb/`](mongodb/), [`duckdb/`](duckdb/), [`aws/`](aws/), [`gcloud/`](gcloud/), [`watchtower/`](watchtower/) below for working examples. A docker plugin can also run on a **remote host's** daemon (over SSH) by targeting a real `host:` in the recipe instead of `host: "_"` — see [Running a docker plugin on a remote host](../../website/docs/plugins-development.md#running-a-docker-plugin-on-a-remote-host).

Enable in honey config:

```yaml
plugins:
  enabled: true
  directory: ""          # default ~/.config/honey/plugins
  allowlist: []          # optional plugin ids; empty = all
  max_memory_mb: 32
  timeout_ms: 30000
  # Optional operator network policy (see below)
  network_deny: false
  network_allow_hosts: []   # if set, each plugin's allowed_hosts must be a subset
```

## API version `honey.plugins/v1`

All exports use JSON input → JSON output. Errors: `{"error":"message"}`.

| Export | Capability | Purpose |
|--------|------------|---------|
| `cue_transform` | `cue_transform` | Transform raw CUE bytes before compile |
| `execute_step` | `custom_step` | Run a recipe `plugin:` step per host |
| `resolve_secret` | `secret` | Resolve refs matching manifest prefixes |
| `unwrap_stack_key` | `stack_unwrap` | Optional stack data-key unwrap |
| `on_step_result` | `hook` | Local hook after step result |

## Plugin manifest (`plugin.yaml`)

Besides `id`, `version`, and `capabilities`, declare what the host may grant:

| Field | Purpose |
|-------|---------|
| `order` | Sort order for `cue_transform` chain (lower first) |
| `secret_ref_prefixes` | Refs like `myvault:path` resolved by `resolve_secret` |
| `allow_host_exec` | Enable `host_exec` host function (local argv-only subprocess) |
| `allow_kv` | Enable `kv` host function (recipe stepkv when step has `kv_tunnel: true`) |
| `allowed_env` | Env var names readable via `get_env` |
| `allowed_hosts` | Hostnames for Extism HTTP from the plugin (empty = no network) |
| `allowed_paths` | WASI filesystem map: guest path → host absolute path |
| `max_http_response_bytes` | Cap per HTTP response (default 4 MiB when memory limit set) |

Example manifest for an HTTP + SQLite-file plugin:

```yaml
id: inventory
version: "0.1.0"
capabilities:
  - cue_transform
  - custom_step
  - secret
allow_host_exec: false
allowed_env:
  - INVENTORY_API_TOKEN
allowed_hosts:
  - inventory.internal.example.com
  - api.github.com
allowed_paths:
  "/var/lib/honey/inventory": "/var/lib/honey/inventory"
max_http_response_bytes: 1048576
secret_ref_prefixes:
  - "inventory:"
```

### Network defaults (Extism)

- Omitted or empty `allowed_hosts` → **no outbound HTTP** (safe default).
- Honey never passes “allow all hosts”; wildcards (`*`) are rejected in YAML.

### Operator network cap (honey config)

If `plugins.network_allow_hosts` is non-empty, every hostname in the plugin manifest must appear in that list (extra deny for ops). Use `plugins.network_deny: true` to block all plugin HTTP even when the manifest lists hosts.

## Host functions

| Function | When available | Purpose |
|----------|----------------|---------|
| `log_info`, `log_warn` | Always | Structured logging on the host |
| `get_env` | `allowed_env` in manifest | Read allowlisted environment variables |
| `host_exec` | `allow_host_exec: true` | Run argv-only subprocess; JSON in/out (no shell) |
| `kv` | `allow_kv: true` and recipe step `kv_tunnel: true` | Get/put/delete keys in the shared per-run stepkv store (same namespace as remote `HONEY_KV_*` steps) |

HTTP from plugin code uses the [Extism Go PDK HTTP API](https://github.com/extism/go-pdk) (`pdk.NewHTTPRequest` / `Send`) against hosts listed in `allowed_hosts`.

`host_exec` input: `{"argv":["echo","ok"],"cwd":"","timeout_ms":5000}`. Output: `{"exit_code":0,"stdout":"…","stderr":"…","error":""}`.

`kv` input: `{"op":"get|put|delete","key":"my-key","value":"..."}`. Output: `{"found":true,"value":"…","error":""}`. For `get`, `found=false` when the key is missing. Keys are shared across plugin steps, remote command/script steps (via `kv_tunnel`), and parallel hosts may race on the same key.

### Recipe KV from Go plugins (`pkg/pluginpdk`)

For Go plugins, use [`pkg/pluginpdk`](../../pkg/pluginpdk) instead of hand-rolling the `kv` wasmimport:

1. **`plugin.yaml`:** `allow_kv: true`
2. **Recipe:** `defaults: { kv_tunnel: true }` (or per-step `kv_tunnel: true`)
3. **Code:**

```go
import "github.com/shareed2k/honey/pkg/pluginpdk"

if err := pluginpdk.KVPut("my-key", "value"); err != nil { /* ... */ }
val, found, err := pluginpdk.KVGet("my-key")
if err := pluginpdk.KVDelete("my-key"); err != nil { /* ... */ }
```

4. **`go.mod`** (example plugin module):

```go
require github.com/shareed2k/honey v0.0.0
replace github.com/shareed2k/honey => ../../..  // path to honey repo root
```

5. **Build:** `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .`

Demo recipe: [`examples/recipe/echo_plugin_kv_demo.cue`](../recipe/echo_plugin_kv_demo.cue). Reference: [`examples/plugins/echo/`](echo/).

Non-Go PDKs: call the `kv` host function with the same JSON shape (see table above).

## What to enable for common plugin types

| Plugin does… | Enable in manifest |
|--------------|-------------------|
| Call REST/GraphQL APIs | `allowed_hosts` + use go-pdk HTTP |
| Read API token from env | `allowed_env` |
| Read local SQLite / files | `allowed_paths` (absolute paths, same guest/host path is fine) |
| Run `psql` / `curl` on operator machine | `allow_host_exec: true` |
| Share scratch state with remote steps | `allow_kv: true` + recipe `kv_tunnel: true` on plugin/command steps |
| Resolve custom secret refs | `secret` capability + `secret_ref_prefixes` |
| Rewrite recipes before compile | `cue_transform` |
| Per-host custom step | `custom_step` |
| Local hook after step | `hook` + `on_step_result` export |

Postgres/MySQL **client libraries inside WASM** are a poor fit (TCP + drivers). Prefer **HTTP APIs**, **`host_exec` + CLI**, the built-in `postgres`/`sqlite` plugins, or a **`runtime: docker`** plugin that execs the real client binary (`mongosh`, `psql`, …) in a container — see below.

Author WASM plugins with the [Extism Go PDK](https://github.com/extism/go-pdk) or other [Extism PDKs](https://extism.org/docs).

## Example Docker-runtime plugins

No WASM build step — each is just `plugin.yaml` (`runtime: docker`) + `plugin.cue` (actions/argv). Copy a directory into your plugins root to use it as-is:

| Plugin | Image | Actions |
|--------|-------|---------|
| [`mongodb/`](mongodb/) | `mongo:latest` | `query`, `eval` — `mongosh` against a URI |
| [`duckdb/`](duckdb/) | `duckdb/duckdb:latest` | `query`, `export_parquet` — bind-mounts `/var/honey/data` |
| [`aws/`](aws/) | `amazon/aws-cli:latest` | `s3_ls`, `s3_cp`, `s3_rm`, `ec2_describe`, `ec2_start`, `ec2_stop` |
| [`gcloud/`](gcloud/) | `gcr.io/google.com/cloudsdktool/cloud-sdk:slim` | `compute_list`, `compute_start`, `compute_stop`, `storage_ls`, `storage_cp`, `storage_rm` |
| [`watchtower/`](watchtower/) | `docker.io/beatkind/watchtower:latest` | `check` (monitor-only, text output), `check_json` (same, single JSON document via `json.v1` — see plugin.cue for the exact shape), `update` — mounts the daemon's Docker socket; with `host: "prod-*"` runs on each server's own daemon to check that server's images |

`gcloud`'s image is **amd64-only** — fails with `exec format error` on Apple Silicon hosts without qemu emulation registered. The other three are multi-arch.

```bash
mkdir -p ~/.config/honey/plugins/mongodb
cp examples/plugins/mongodb/plugin.yaml examples/plugins/mongodb/plugin.cue ~/.config/honey/plugins/mongodb/
```

Requires `honey-plugin-init` built alongside the `honey` binary — see [Docker runtime plugins](../../website/docs/plugins-development.md#docker-runtime-plugins) for the full manifest/`plugin.cue` schema and packaging notes.

## Example: echo

**Walkthrough:** [echo/README.md](echo/README.md) — install, enable plugins, run `cue-exec` with the demo recipe.

Source: [`examples/plugins/echo/`](echo/). Build and refresh the committed test binary:

```bash
task build-plugin-examples
```

Install for local use:

```bash
mkdir -p ~/.config/honey/plugins/echo
cp examples/plugins/echo/plugin.yaml ~/.config/honey/plugins/echo/
cp examples/plugins/echo/plugin.wasm ~/.config/honey/plugins/echo/
```

Commit `internal/plugins/testdata/echo/plugin.wasm` when changing the echo plugin so CI runs Extism tests without rebuilding.

List loaded plugins: `honey plugins list`
