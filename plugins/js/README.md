# js plugin

WASM `custom_step` plugin that runs a user **JavaScript** snippet in an embedded
[goja](https://github.com/dop251/goja) interpreter (pure Go, ES5.1 + much ES6).
Use it for recipe glue logic that is awkward in shell: parsing, branching,
shaping JSON for downstream steps.

The interpreter is sandboxed — scripts can only touch the narrow host API the
plugin injects, gated by `plugin.yaml`.

## Action `run`

```cue
plugin: {
	id:     "js"
	action: "run"
	config: {
		code:       "<javascript>"
		args:       {key: "value"}  // optional, bound to global `args`
		timeout_ms: 30000           // optional, default 30s
	}
}
```

The script's completion value becomes the step's **stdout**: a string passes
through verbatim, anything else is JSON-encoded (so a script returning
`JSON.stringify(obj)` and one returning `obj` produce the same output).
`undefined`/`null` yields empty stdout. Consume it downstream with `env_from` /
`loop_from`.

### Host API exposed to scripts

| Global | Signature | Notes |
|--------|-----------|-------|
| `host.remote_exec(script)` | `-> {stdout, stderr, exit_code, failed, changed, error}` | runs `/bin/sh` on the target (needs `allow_remote_exec`) |
| `kv.get(key)` | `-> string \| undefined` | shared per-run KV (needs `allow_kv`) |
| `kv.put(key, value)` | `-> void` | |
| `log(msg)` | `-> void` | host log line |
| `args` | object | from `config.args` |

### Dry-run

Without `--execute` the script is still evaluated (catches syntax/logic errors)
but side-effecting host calls are stubbed: `host.remote_exec` returns an empty
changed result and `kv.put` is a no-op. `kv.get` still reads so logic can branch.

## Build & install

```sh
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
mkdir -p ~/.config/honey/plugins/js
cp plugin.yaml plugin.wasm ~/.config/honey/plugins/js/
```

Enable `plugins.enabled: true`. See
[examples/recipe/js_step.cue](../../examples/recipe/js_step.cue).

## Notes

- The wasm module is large (~19 MB) because it embeds a full JS engine; first
  instantiation per run is correspondingly slower than a tiny plugin.
- `timeout_ms` interrupts runaway scripts (e.g. `while(true){}`).
- Pure logic lives in the `jsrun/` sub-package (host-testable); `main.go` is the
  thin wasm glue that bridges `jsrun.HostAPI` to honey host functions.
