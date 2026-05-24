// Example recipe using the bash WASM module (host-mediated remote_exec).
//
// Install built-in modules:
//   make build-plugin-modules
//   mkdir -p ~/.config/honey/plugins/bash
//   cp examples/plugins/bash/plugin.yaml examples/plugins/bash/plugin.wasm ~/.config/honey/plugins/bash/
//
// Requires plugins.enabled: true in honey config.
//
//   honey cue-exec examples/recipe/bash_module_demo.cue "web-*"
//   honey cue-exec --execute examples/recipe/bash_module_demo.cue "web-*"

recipe: {
	name: "bash_module_demo"
	steps: [{
		host: "*"
		plugin: {
			id: "bash"
			action: "run"
			config: {
				script: """
					set -euo pipefail
					hostname
					uptime
				"""
			}
		}
	}]
}
