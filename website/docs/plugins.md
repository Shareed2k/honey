---
id: plugins
title: Plugins
---

Honey supports plugins that extend [CUE recipes](./cue-recipes.md) with custom steps, secret backends, and log transforms, in two runtimes:

- **`wasm`** (default) — runs locally on the operator's machine inside an [Extism](https://extism.org/) sandbox with explicit permission grants.
- **`docker`** — runs a real binary (`mongosh`, `aws`, `gcloud`, `duckdb`, `ffmpeg`, …) inside a container, for tools that can't reasonably be reimplemented or wrapped in WASM.

See [Plugin development](./plugins-development.md) for the full schema of both.

## Enable plugins

Add a `plugins` block to your `honey.yaml`:

```yaml
plugins:
  enabled: true
  directory: ""                  # default: ~/.config/honey/plugins
  allowlist: []                  # optional plugin ids; empty = all discovered
  max_memory_mb: 32
  timeout_ms: 30000
  network_deny: false
  network_allow_hosts: []
```

Plugins are **disabled by default** — set `enabled: true` to activate them.

## Install a plugin

`honey plugins install` downloads or copies a plugin into your plugins directory and validates its manifest.

```bash
# From a GitHub release URL
honey plugins install https://github.com/shareed2k/honey/releases/download/v1.2.3/honey-plugin-bash-wasip1-wasm.tar.gz

# From a local directory (must contain plugin.yaml + plugin.wasm)
honey plugins install ./my-plugin/

# From a local archive
honey plugins install ./my-plugin.tar.gz

# Force reinstall (overwrite existing)
honey plugins install --force ./my-plugin/

# Override the install directory
honey plugins install --dir /custom/plugins ./my-plugin/
```

The plugin is installed to `<plugins-dir>/<plugin-id>/`. The plugin id comes from the `id` field in `plugin.yaml`.

## Built-in plugins

Honey ships pre-built releases for the following plugins. Install any of them from a release URL with `honey plugins install`.

| Plugin | Capability | Description |
|--------|-----------|-------------|
| `bash` | `custom_step` | Run bash scripts on remote hosts |
| `shell` | `custom_step` | Run POSIX shell commands on remote hosts |
| `copy` | `custom_step` | Copy files between locations |
| `template` | `custom_step`, `cue_transform` | Render Go templates and push results to hosts |
| `file` | `custom_step` | Read and write files on remote hosts |
| `service` | `custom_step` | Manage systemd services on remote hosts |
| `postgres` | `custom_step` | Run SQL against Postgres instances |
| `sqlite` | `custom_step` | Run embedded SQLite queries inside WASM against mounted DB files |
| `rclone` | `custom_step` | Transfer files via rclone |
| `cve-scanner` | `custom_step` | Scan hosts for CVEs (grype/trivy) and apply security patches — see [Vulnerability & patch management](./vulnerability-management.md) |
| `js` | `custom_step` | Run sandboxed JavaScript (goja) with a capability-gated host API (`host.remote_exec`, `kv`, `log`) |

## Example Docker-runtime plugins

No build step — just `plugin.yaml` (`runtime: docker`) + `plugin.cue` (actions/argv). See [`examples/plugins/`](https://github.com/shareed2k/honey/tree/main/examples/plugins):

| Plugin | Image | Actions |
|--------|-------|---------|
| `mongodb` | `mongo:latest` | `query`, `eval` |
| `duckdb` | `duckdb/duckdb:latest` | `query`, `export_parquet` |
| `aws` | `amazon/aws-cli:latest` | `s3_ls`, `s3_cp`, `s3_rm`, `ec2_describe`, `ec2_start`, `ec2_stop` |
| `gcloud` | `gcr.io/google.com/cloudsdktool/cloud-sdk:slim` | `compute_list`, `compute_start`, `compute_stop`, `storage_ls`, `storage_cp`, `storage_rm` |

`gcloud`'s image is **amd64-only** — fails with `exec format error` on Apple Silicon hosts unless your Docker daemon has qemu emulation registered. The other three are multi-arch.

## List installed plugins

```bash
honey plugins list
honey plugins list --config ~/.config/honey/config.yaml
```

When `plugins.enabled` is false, the command shows a reminder to enable plugins. When enabled, it lists each plugin's id, version, capabilities, and disk path as JSON.

## Manual installation

If `honey plugins install` is not available or you prefer manual control:

```bash
mkdir -p ~/.config/honey/plugins/myplugin
cp plugin.yaml ~/.config/honey/plugins/myplugin/
cp plugin.wasm ~/.config/honey/plugins/myplugin/
```

The directory name does not need to match the plugin id — Honey reads `plugin.yaml` to discover the id. A `runtime: wasm` (default) plugin directory must contain `plugin.yaml` and `plugin.wasm`; a `runtime: docker` plugin directory must contain `plugin.yaml` and `plugin.cue` instead (no wasm module).

## Using plugins in recipes

Enable in config, then reference a plugin by id in a CUE recipe `plugin:` step:

```cue
recipe: {
  steps: [
    {
      host: "web-*"
      plugin: {
        id:     "bash"
        action: "run"
        config: {script: "systemctl restart nginx"}
      }
    }
  ]
}
```

See [Plugin development](./plugins-development.md) for the full step schema and how to write your own plugin.

## Security

- Plugins run locally on the operator machine, not on remote hosts (unless the plugin itself makes outbound calls via `allow_remote_exec` or `allow_host_exec`).
- Review `plugin.yaml` permissions before installing — check `allow_host_exec`, `allow_remote_exec`, `allowed_hosts`, and `allowed_paths`.
- Use `plugins.allowlist` in your config to restrict which plugin ids may load:

  ```yaml
  plugins:
    enabled: true
    allowlist: [bash, template]
  ```
