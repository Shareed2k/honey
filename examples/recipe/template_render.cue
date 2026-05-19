// Graph recipe: command → template (capture) → command via env_from.from_output.
//
// Template steps run locally (host: "_"). template.output registers a capture name (not a step id).
// Downstream steps depend on the template step id and pull stdout via from_output.
//
// Validate:
//   honey cue-validate examples/recipe/template_render.cue
// Dry-run:
//   honey cue-exec examples/recipe/template_render.cue "*"
recipe: {
	name: "template-render"
	type: "graph"
	steps: [
		{
			id:      "fetch"
			host:    "*"
			command: "echo -n honey"
		},
		{
			id:      "render"
			host:    "_"
			depends: ["fetch"]
			env_from: [{
				step: "fetch"
				map: HOSTNAME: "stdout"
			}]
			template: {
				template: "greeting={{ .HOSTNAME | default \"unknown\" }}\n"
				data: {
					note: "static"
				}
				output: "RESULT"
			}
		},
		{
			id:      "use"
			host:    "*"
			depends: ["render"]
			env_from: [{
				from_output: "RESULT"
				map: CFG: "stdout"
			}]
			command: "echo \"$CFG\""
		},
	]
}
