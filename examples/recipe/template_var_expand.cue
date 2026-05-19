// Graph recipe: ${VAR} expansion in template.data values (not in the Go template body).
//
// After env_from merges dependency stdout into data keys, string values like "${TAG}"
// are expanded before text/template runs. The template body still uses {{ .field }} syntax;
// literals such as ${OTHER} in the template string are preserved.
//
// Validate:
//   honey cue-validate examples/recipe/template_var_expand.cue
recipe: {
	name: "template-var-expand"
	type: "graph"
	steps: [
		{
			id:      "fetch"
			host:    "*"
			command: "echo -n shard"
		},
		{
			id:      "render"
			host:    "_"
			depends: ["fetch"]
			env_from: [{
				step: "fetch"
				map: TAG: "stdout"
			}]
			template: {
				template: "message={{ .msg }}\n"
				data: {
					msg: "prefix-${TAG}-suffix"
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
				map: LINE: "stdout"
			}]
			command: "echo \"$LINE\""
		},
	]
}
