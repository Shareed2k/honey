// Graph recipe: per-host template (host: "*") with host-scoped env_from.
//
// Each host runs its own template render using that host's dependency stdout.
// template.output capture is only allowed with host: "_" — this example uses env_from.step
// to pass rendered text to the next command.
//
// Validate:
//   honey cue-validate examples/recipe/template_per_host.cue
recipe: {
	name: "template-per-host"
	type: "graph"
	steps: [
		{
			id:      "fetch"
			host:    "*"
			command: "hostname -s"
		},
		{
			id:      "render"
			host:    "*"
			depends: ["fetch"]
			env_from: [{
				step: "fetch"
				map: SHORT: "stdout"
			}]
			template: {
				template: "short={{ .SHORT | default \"?\" }}\n"
				data: {}
			}
		},
		{
			id:      "use"
			host:    "*"
			depends: ["render"]
			env_from: [{
				step: "render"
				map: LINE: "stdout"
			}]
			command: "echo \"[$HONEY_HOST_NAME] $LINE\""
		},
	]
}
