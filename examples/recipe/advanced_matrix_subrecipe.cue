// This recipe demonstrates combining matrix execution, sub-recipes, and step-level assertions.
// 
// Run:
//   honey cue-exec examples/recipe/advanced_matrix_subrecipe.cue "local" --provider local --execute

recipe: {
	name: "advanced-matrix-subrecipe"
	type: "graph"
	steps: [
		{
			id: "setup-env"
			host: "*"
			command: "echo 'Setup complete'"
			assert: [{
				exit_code: 0
			}]
		},
		{
			id: "deploy-components"
			depends: ["setup-env"]
			host: "*"
			matrix: {
				target_env: ["staging", "production"]
				component: ["api", "worker"]
			}
			recipe: {
				path: "matrix_worker.cue"
				prompts: {
					APP_ENV: "${target_env}"
					COMPONENT: "${component}"
				}
			}
		}
	]
}