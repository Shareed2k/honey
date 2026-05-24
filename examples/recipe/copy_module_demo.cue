// Example recipe using the copy WASM module (SFTP upload via remote_upload).
//
// Local paths are relative to this recipe file's directory (see file_transfer.cue).
//
// Install: make build-plugin-modules && cp examples/plugins/copy/* ~/.config/honey/plugins/copy/
//
//   honey cue-exec examples/recipe/copy_module_demo.cue "*"
//   honey cue-exec --execute examples/recipe/copy_module_demo.cue "*"
recipe: {
	name: "copy-module-demo"
	steps: [{
		host: "*"
		plugin: {
			id: "copy"
			action: "put"
			config: {
				local:  "./assets/index.html"
				remote: "/tmp/honey-copy-demo/index.html"
			}
		}
	}]
}
