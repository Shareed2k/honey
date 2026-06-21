// This is a sub-recipe that will be called multiple times by the parent's matrix.
recipe: {
	name: "matrix-worker"
	type: "graph"
	defaults: {
		prompts: {
			APP_ENV: {
				description: "The environment"
				type: "string"
				required: true
			}
			COMPONENT: {
				description: "The component to deploy"
				type: "string"
				required: true
			}
		}
	}
	steps: [
		{
			id: "work"
			host: "*"
			command: "echo \"Deploying $COMPONENT to $APP_ENV\""
			assert: [{
				regex: "Deploying .+"
			}]
		}
	]
}