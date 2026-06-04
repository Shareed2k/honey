// examples/recipe/dynamic_loops_facts.cue
// Demonstrates automatic fact gathering in CEL and runtime loop_from execution.
// Run: honey cue-exec examples/recipe/dynamic_loops_facts.cue "*"
recipe: {
	name: "loops-and-facts"
	type: "graph"
	defaults: {
		// Tells honey to run a probe (uname, etc) before starting the graph
		// Exposes facts.* variables to CEL 'when' conditions
		gather_facts: true
	}
	steps: [
		{
			id: "fetch_users"
			// Only run this step on linux machines
			when: "facts.os == 'linux'"
			host: "*"
			// Simulate an API call returning JSON array of objects
			command: """
			cat <<EOF
			[{"user":"alice", "uid": 1001}, {"user":"bob", "uid": 1002}]
			EOF
			"""
		},
		{
			id: "process_users"
			depends: ["fetch_users"]
			host: "*"
			// Loop over the output of the fetch_users step
			loop_from: {
				step: "fetch_users"
				extract: ".[].user" // jq expression to yield an array of strings
			}
			// ${item} is injected into the environment for each loop iteration
			command: "echo \"Processing user: ${item}\""
		}
	]
}
