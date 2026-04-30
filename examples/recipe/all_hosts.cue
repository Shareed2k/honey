// Run the same command on every host returned by your search (rows with PrimaryIP only).
//
//   honey cue-validate examples/recipe/all_hosts.cue
//   honey cue-exec examples/recipe/all_hosts.cue kafka        # dry-run
//   honey cue-exec --execute examples/recipe/all_hosts.cue kafka
recipe: {
	name: "all-in-current-search"
	steps: [
		{host: "*", command: "hostname"},
		{host: "*", command: "uptime"},
	]
}
