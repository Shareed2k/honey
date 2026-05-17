// Graph-mode recipe: parallel branches after a shared fetch step.
// Dry-run: honey cue-exec examples/recipe/graph_parallel.cue "*"
recipe: {
	name: "parallel-restart"
	type: "graph"
	steps: [
		{
			id:      "fetch"
			host:    "*"
			command: "echo fetch"
		},
		{
			id:      "restart_a"
			host:    "*"
			depends: ["fetch"]
			command: "echo restart_a"
		},
		{
			id:      "restart_b"
			host:    "*"
			depends: ["fetch"]
			command: "echo restart_b"
		},
		{
			id:      "verify"
			host:    "*"
			depends: ["restart_a", "restart_b"]
			command: "echo verify"
		},
		{
			id:      "summarize"
			host:    "_"
			depends: ["verify"]
			ai: { prompt: "Summarize the run." }
		},
	]
}
