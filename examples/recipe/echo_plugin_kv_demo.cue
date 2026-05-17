// Demo: echo WASM plugin + shared recipe KV (stepkv).
//
// Step 1 runs on the operator via Extism (`kv_ping` uses the `kv` host function).
// Step 2 reads the same key on each remote host via HONEY_KV_* (SSH forward / k8s bridge).
//
// Prerequisites:
//   - plugins.enabled: true; echo under ~/.config/honey/plugins/echo/ with allow_kv: true
//   - curl on remote hosts for step 2
//
//   honey cue-validate examples/recipe/echo_plugin_kv_demo.cue
//   honey cue-exec examples/recipe/echo_plugin_kv_demo.cue "<search>"
//   honey cue-exec --execute examples/recipe/echo_plugin_kv_demo.cue "<search>"
//
// See also: examples/recipe/kv_tunnel_example.cue, examples/plugins/echo/README.md
recipe: {
	name: "echo-plugin-kv-demo"

	defaults: { kv_tunnel: true }

	steps: [
		{
			host: "*"
			plugin: {
				id:     "echo"
				action: "kv_ping"
			}
		},
		{
			host: "*"
			command: "set -e; curl -fsS -o /dev/null -H \"Authorization: Bearer ${HONEY_KV_TOKEN}\" \"${HONEY_KV_URL}/v1/kv/__health\"; V=\"$(curl -fsS -H \"Authorization: Bearer ${HONEY_KV_TOKEN}\" \"${HONEY_KV_URL}/v1/kv/echo-kv-ping\")\"; echo \"remote read echo-kv-ping: ${V}\"; test \"${V}\" = pong"
		},
	]
}
