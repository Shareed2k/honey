// Linear recipe: single local template step (no graph capture; for dry-run / smoke tests).
//
// template.output is informational in linear mode; use type: "graph" for capture + from_output.
//
// Validate:
//   honey cue-validate examples/recipe/template_linear.cue
recipe: {
	name: "template-linear"
	steps: [
		{
			host: "*"
			command: "uname -n"
		},
		{
			host: "_"
			template: {
				template: "# static config\napp=honey\nnote={{ .note }}\n"
				data: {
					note: "generated locally"
				}
			}
		},
	]
}
