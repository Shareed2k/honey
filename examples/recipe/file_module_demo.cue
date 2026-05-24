// Example recipe using the file WASM module (remote_stat + remote_exec).
//
// Ensures a directory exists, touches a marker file, then verifies with bash.
//
// Install: make build-plugin-modules && cp examples/plugins/file/* ~/.config/honey/plugins/file/
//
//   honey cue-exec examples/recipe/file_module_demo.cue "*"
//   honey cue-exec --execute examples/recipe/file_module_demo.cue "*"
recipe: {
	name: "file-module-demo"
	steps: [
		{
			host: "*"
			plugin: {
				id: "file"
				action: "manage"
				config: {
					path:  "/tmp/honey-file-demo"
					state: "directory"
				}
			}
		},
		{
			host: "*"
			plugin: {
				id: "file"
				action: "manage"
				config: {
					path:  "/tmp/honey-file-demo/.managed"
					state: "touch"
				}
			}
		},
		{
			host: "*"
			plugin: {
				id: "bash"
				action: "run"
				config: {
					script: """
						set -eu
						test -d /tmp/honey-file-demo
						test -f /tmp/honey-file-demo/.managed
						ls -la /tmp/honey-file-demo
					"""
				}
			}
		},
	]
}
