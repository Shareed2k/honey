// Graph recipe chaining WASM modules: file → copy → template → bash verify.
//
// Demonstrates plugin steps with id/depends in graph mode (parallel waves).
//
// Install all built-in modules:
//   make build-plugin-modules
//   for m in file copy template bash; do
//     mkdir -p ~/.config/honey/plugins/$m
//     cp examples/plugins/$m/plugin.yaml examples/plugins/$m/plugin.wasm ~/.config/honey/plugins/$m/
//   done
//
//   honey cue-exec examples/recipe/graph_plugin_modules.cue "*"
//   honey cue-exec --execute examples/recipe/graph_plugin_modules.cue "*"
recipe: {
	name: "graph-plugin-modules"
	type: "graph"
	steps: [
		{
			id:   "layout"
			host: "*"
			plugin: {
				id: "file"
				action: "manage"
				config: {
					path:  "/tmp/honey-graph-demo"
					state: "directory"
				}
			}
		},
		{
			id:      "static"
			host:    "*"
			depends: ["layout"]
			plugin: {
				id: "copy"
				action: "put"
				config: {
					local:  "./assets/index.html"
					remote: "/tmp/honey-graph-demo/index.html"
				}
			}
		},
		{
			id:      "config"
			host:    "*"
			depends: ["layout"]
			plugin: {
				id: "template"
				action: "put"
				config: {
					remote: "/tmp/honey-graph-demo/app.env"
					template: """
						app={{ .app | quote }}
						static=/tmp/honey-graph-demo/index.html
						"""
					data: {
						app: "graph-demo"
					}
				}
			}
		},
		{
			id:      "verify"
			host:    "*"
			depends: ["static", "config"]
			plugin: {
				id: "bash"
				action: "run"
				config: {
					script: """
						set -eu
						test -f /tmp/honey-graph-demo/index.html
						test -f /tmp/honey-graph-demo/app.env
						echo "=== app.env ==="
						cat /tmp/honey-graph-demo/app.env
					"""
				}
			}
		},
	]
}
