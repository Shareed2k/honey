// Example recipe using the shell WASM module (/bin/sh via remote_exec).
//
// Install: make build-plugin-modules && cp examples/plugins/shell/* ~/.config/honey/plugins/shell/
//
//   honey cue-exec examples/recipe/shell_module_demo.cue "*"
//   honey cue-exec --execute examples/recipe/shell_module_demo.cue "*"
recipe: {
	name: "shell-module-demo"
	steps: [{
		host: "*"
		plugin: {
			id: "shell"
			action: "run"
			config: {
				script: """
					set -eu
					echo "host=$HONEY_HOST_NAME ip=$HONEY_HOST_PRIMARY_IP"
					uname -srm
				"""
			}
		}
	}]
}
