// Example recipe using the template WASM module (template_render + remote_upload).
//
// The template body uses Go text/template + slim-sprig on the operator host.
// Time/random sprig funcs (now, date, uuidv4, …) are blocked — same as native template steps.
// Rendered content is uploaded to each target via SFTP.
//
// Install: make build-plugin-modules && cp examples/plugins/template/* ~/.config/honey/plugins/template/
//
//   honey cue-exec examples/recipe/template_module_demo.cue "*"
//   honey cue-exec --execute examples/recipe/template_module_demo.cue "*"
recipe: {
	name: "template-module-demo"
	steps: [{
		host: "*"
		plugin: {
			id: "template"
			action: "put"
			config: {
				remote: "/tmp/honey-template-demo/app.env"
				template: """
					# Managed by honey template module
					app={{ .app | quote }}
					tier={{ .tier | quote }}
					managed_by={{ .managed_by | default "honey" | quote }}
					"""
				data: {
					app:         "honey-demo"
					tier:        "staging"
					managed_by:  "template-module-demo"
				}
			}
		}
	}]
}
