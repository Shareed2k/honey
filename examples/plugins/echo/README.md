# Echo plugin — run with honey

The **echo** plugin is a minimal WASM example for honey’s Extism plugin system. It demonstrates `cue_transform`, custom `plugin:` steps, `echo:` secret refs, and (with `allow_host_exec: true`) the `host_exec` host function.

## Prerequisites

- honey built from this repo (or installed with plugin support)
- A honey config file (e.g. `~/.config/honey/config.yaml`)
- Host inventory via `honey search` (backends configured in YAML)

## 1. Build and install the plugin

From the repository root:

```bash
make build-plugin-examples
```

Install into the default plugin directory:

```bash
mkdir -p ~/.config/honey/plugins/echo
cp examples/plugins/echo/plugin.yaml ~/.config/honey/plugins/echo/
cp examples/plugins/echo/plugin.wasm ~/.config/honey/plugins/echo/
```

The manifest sets `allow_host_exec: true` so the `host_exec` action works when installed from this tree.

## 2. Enable plugins in honey config

Add to your honey YAML (`~/.config/honey/config.yaml` or pass `--config`):

```yaml
plugins:
  enabled: true
  # directory: ""     # default ~/.config/honey/plugins
  # allowlist: []     # empty = load every plugin subdirectory
  max_memory_mb: 32
  timeout_ms: 30000
```

Verify the plugin loaded:

```bash
honey plugins list --config ~/.config/honey/config.yaml
```

Expected: one entry with `id: echo`, capabilities including `cue_transform` and `custom_step`.

## 3. Automatic CUE transform

Whenever plugins are enabled, honey runs the `cue_transform` chain **before** compiling a recipe. The echo plugin prepends this line to your `.cue` file:

```cue
// honey-echo-transform
```

You do not call this export yourself; it applies to `honey cue-validate`, `honey cue-exec`, the TUI, and the web cue-exec API.

## 4. Example recipe

Use the included demo recipe:

[`examples/recipe/echo_plugin_demo.cue`](../../recipe/echo_plugin_demo.cue)

Or define your own:

```cue
recipe: {
	name: "echo-plugin-demo"
	steps: [
		{
			host: "*"
			plugin: {
				id:     "echo"
				action: "noop"
			}
		},
	]
}
```

### Dry-run (default)

Shows the plan; plugin steps report `stdout: dry-run` without side effects:

```bash
honey cue-exec examples/recipe/echo_plugin_demo.cue --config ~/.config/honey/config.yaml
```

Add the same host filters you use for `honey search` (name argument, `--name`, backends, etc.).

### Execute

Runs plugin steps for each matching host:

```bash
honey cue-exec --execute examples/recipe/echo_plugin_demo.cue --config ~/.config/honey/config.yaml
```

With `action: "noop"`, stdout is `executed` per host.

### `host_exec` action

The demo recipe includes a second step with `action: "host_exec"`. On execute, the plugin calls the host `host_exec` function with `argv: ["echo", "ok"]`; you should see `ok` in the step output.

```bash
honey cue-exec --execute examples/recipe/echo_plugin_demo.cue --config ~/.config/honey/config.yaml
```

### `kv_ping` action (plugin + recipe KV)

Echo writes key `echo-kv-ping` = `pong` using [`pkg/pluginpdk`](../../../pkg/pluginpdk) (`KVPut` / `KVGet`), then a remote step reads it via `HONEY_KV_*`. Use **`defaults.kv_tunnel: true`** in the recipe.

Recipe: [`examples/recipe/echo_plugin_kv_demo.cue`](../../recipe/echo_plugin_kv_demo.cue)

Requires `allow_kv: true` in `plugin.yaml` (included in this tree). Remote step 2 needs `curl`.

```bash
honey cue-exec --execute examples/recipe/echo_plugin_kv_demo.cue --config ~/.config/honey/config.yaml "<search>"
```

## 5. Echo secret refs (demo only)

Echo resolves recipe secret refs that start with `echo:`. The suffix becomes the env value at execute time.

```cue
recipe: {
	name: "echo-secret-demo"
	steps: [
		{
			host:    "*"
			command: "echo $DEMO"
			secrets: {
				DEMO: "echo:my-plaintext-value"
			}
		},
	]
}
```

Production recipes should use `secure:v1:…` for real secrets; `echo:` is for testing the plugin secret backend only.

## 6. Echo plugin behavior reference

| Export / feature | Behavior |
|------------------|----------|
| `cue_transform` | Prepends `// honey-echo-transform\n` to raw CUE bytes |
| `execute_step` + `execute: false` | `success: true`, `stdout: "dry-run"` |
| `execute_step` + `action: "noop"` + execute | `stdout: "executed"` |
| `execute_step` + `action: "host_exec"` + execute | Runs host `echo ok`, returns stdout |
| `execute_step` + `action: "kv_ping"` + execute + `kv_tunnel` | Put/get `echo-kv-ping` via `pluginpdk`; stdout `pong` |
| `resolve_secret` | `echo:VALUE` → plaintext `VALUE` |

## 7. Troubleshooting

| Symptom | Check |
|---------|--------|
| `plugins list` empty | `plugins.enabled: true`, files under `~/.config/honey/plugins/echo/` |
| Plugin step error “plugins disabled” | Enable plugins in config |
| `host_exec` fails | `allow_host_exec: true` in `plugin.yaml` |
| No hosts matched | Widen `honey search` filters; step `host` must match inventory |
| Transform not visible | Transform runs on bytes before compile; use `cue-validate` on a small file and inspect errors or add a deliberate syntax check |

## 8. Related docs

- [Plugins overview](../README.md) — API version, host functions, authoring
- [Recipe examples](../../recipe/README.md) — general CUE recipe usage
