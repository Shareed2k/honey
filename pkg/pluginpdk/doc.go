// Package pluginpdk provides helpers for honey WASM plugins built with the Extism Go PDK.
//
// Recipe KV (stepkv) is optional per plugin and per recipe run:
//
//   - plugin.yaml: allow_kv: true
//   - recipe step or defaults: kv_tunnel: true
//
// Without both, KV* functions return an error from the host (e.g. "kv not available for this call").
// RemoteExec/RemoteUpload/RemoteStat require allow_remote_exec / allow_sftp in plugin.yaml.
// PostgresQuery/PostgresExec require allow_postgres in plugin.yaml.
// Keys are shared with remote command/script steps that use HONEY_KV_URL on the same cue-exec run.
//
// Build plugins for WASI:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
//
// Example recipe: examples/recipe/echo_plugin_kv_demo.cue
// Reference plugin: examples/plugins/echo/
package pluginpdk
