// Run custom JavaScript in a recipe step via the js WASM plugin (goja).
//
// The script's return value becomes the step's stdout (a string passes through
// verbatim; anything else is JSON-encoded) so later steps can consume it with
// env_from / loop_from. The script may call a capability-gated host API:
//   host.remote_exec(script) -> {stdout, stderr, exit_code, failed, changed}
//   kv.get(key) / kv.put(key, value)
//   log(msg)
//   args.<key>   (from config.args)
//
// Requires plugins.enabled: true and js installed under
// ~/.config/honey/plugins/js/.
//
//   honey cue-exec examples/recipe/js_step.cue "<search-filter>"
//   honey cue-exec --execute examples/recipe/js_step.cue "<search-filter>"
recipe: {
	name: "js-step"
	type: "graph"
	steps: [
		// Run JS that shells out via host.remote_exec and returns a JSON object.
		{
			id:   "probe"
			host: "*"
			plugin: {
				id:     "js"
				action: "run"
				config: {
					args: {label: "cpu-check"}
					code: """
						let cpus = host.remote_exec('nproc').stdout.trim();
						let kernel = host.remote_exec('uname -r').stdout.trim();
						log('probed ' + args.label);
						JSON.stringify({cpus: cpus, kernel: kernel});
						"""
				}
			}
		},
		// Consume the previous step's JSON output via env_from.
		{
			id:      "report"
			host:    "*"
			depends: ["probe"]
			env_from: [{
				step: "probe"
				extract: CPUS: ".cpus"
			}]
			command: "echo \"host has ${CPUS} cpus\""
		},
	]
}
