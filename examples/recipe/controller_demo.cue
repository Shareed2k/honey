// Controller recipe: instead of a fixed order (linear/graph), you declare TASKS
// (goals) and expose each step as a tool; an LLM decides which steps to run, in
// what order, until every task is settled. The LLM can only choose among these
// operator-authored steps — every step it picks still runs through honey's normal
// host policy / when / command-risk gates.
//
// Requires an OpenAI-compatible endpoint:
//   OPENAI_API_KEY (required), OPENAI_BASE_URL (optional), OPENAI_MODEL / controller.model.
//
//   honey cue-validate examples/recipe/controller_demo.cue
//   OPENAI_API_KEY=… honey cue-exec examples/recipe/controller_demo.cue <inventory-host> --execute
//
// A dry-run (omit --execute) prints the plan without calling the LLM. A controller
// run costs LLM tokens and is nondeterministic; max_turns bounds it.
recipe: {
	name: "controller-demo"
	type: "controller"
	controller: {max_turns: 8}
	tasks: [
		{name: "time_reported", description: "the current server time has been reported"},
		{name: "user_reported", description: "the OS user running the commands has been reported"},
	]
	steps: [
		{id: "server_time", description: "print the current server time", host: "_", command: "date"},
		{id: "current_user", description: "print the current OS user", host: "_", command: "whoami"},
	]
}
