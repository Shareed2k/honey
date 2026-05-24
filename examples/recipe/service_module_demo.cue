// Example recipe using the service WASM module (systemctl via remote_exec).
//
// Ensures a unit is in the started state. Adjust config.name for your hosts.
// On dry-run, the module returns a plan without calling systemctl.
//
// Install: make build-plugin-modules && cp examples/plugins/service/* ~/.config/honey/plugins/service/
//
//   honey cue-exec examples/recipe/service_module_demo.cue "*"
//   honey cue-exec --execute examples/recipe/service_module_demo.cue "*"
recipe: {
	name: "service-module-demo"
	steps: [{
		host: "*"
		plugin: {
			id: "service"
			action: "manage"
			config: {
				name:  "cron"
				state: "started"
			}
		}
	}]
}
