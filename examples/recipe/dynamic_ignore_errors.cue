// examples/recipe/dynamic_ignore_errors.cue
// Demonstrates ignore_errors preventing pipeline failure.
// Run: honey cue-exec examples/recipe/dynamic_ignore_errors.cue "*"
recipe: {
	name: "ignore-errors-demo"
	type: "graph"
	steps: [
		{
			id: "risky_operation"
			host: "*"
			// This command will fail
			command: "ls /nonexistent_directory_that_will_fail"
			// But we tell honey to ignore the failure and mark the step as successful
			ignore_errors: true
		},
		{
			id: "follow_up"
			depends: ["risky_operation"]
			host: "*"
			// This will still execute because risky_operation's error was ignored
			command: "echo 'The risky operation finished (or failed gracefully). Proceeding!'"
		}
	]
}
