recipe: {
	name: "assertions-demo"
	type: "graph"
	steps: [
		{
			id: "json-assert"
			host: "*"
			plugin: {
				id: "bash"
				action: "run"
				config: {
					script: "echo '{\"status\":\"healthy\"}'"
				}
			}
			assert: [{
				json_path: "status"
				expected_value: "healthy"
			}]
		},
		{
			id: "fail-assert-but-success-code"
			depends: ["json-assert"]
			host: "*"
			plugin: {
				id: "bash"
				action: "run"
				config: {
					script: "exit 1"
				}
			}
			ignore_errors: true // prevent immediately stopping
			assert: [{
				exit_code: 1
			}]
		}
	]
}
