recipe: {
	name: "matrix-demo"
	type: "graph"
	steps: [
		{
			id: "echo-matrix"
			host: "*"
			matrix: {
				db: ["postgres", "mysql"]
				version: ["v1", "v2"]
			}
			command: "echo '{\"db\": \"'\"$db\"'\", \"version\": \"'\"$version\"'\"}'"
		},
		{
			id: "collect-results"
			depends: ["echo-matrix"]
			host: "*"
			env_from: [{
				step: "echo-matrix"
				map: ALL_RESULTS: "stdout"
			}]
			command: "echo \"Got matrix outputs: $ALL_RESULTS\""
		}
	]
}
