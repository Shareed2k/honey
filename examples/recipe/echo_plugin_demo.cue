// Demo recipe for the echo WASM plugin (see examples/plugins/echo/README.md).
//
//   honey plugins list --config ~/.config/honey/config.yaml
//   honey cue-exec examples/recipe/echo_plugin_demo.cue <search-filter>
//   honey cue-exec --execute examples/recipe/echo_plugin_demo.cue <search-filter>
//
// Requires plugins.enabled: true and echo installed under ~/.config/honey/plugins/echo/.
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
		{
			host: "*"
			plugin: {
				id:     "echo"
				action: "host_exec"
			}
		},
	]
}
