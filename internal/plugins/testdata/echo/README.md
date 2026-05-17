# echo test plugin

`plugin.wasm` is a prebuilt Extism plugin used by `internal/plugins` tests.

## Rebuild

When changing the echo plugin source, rebuild WASM and copy it here:

```bash
# From your echo plugin build output:
cp /path/to/echo.wasm plugin.wasm
go test ./internal/plugins/... -count=1
```

Manifest for the plugin is `plugin.yaml` in this directory.
