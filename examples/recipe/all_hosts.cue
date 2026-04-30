// Run the same command on every host returned by your search (rows with PrimaryIP only).
//
//   hostctl cue-validate examples/recipe/all_hosts.cue
//   hostctl cue-exec examples/recipe/all_hosts.cue kafka        # dry-run
//   hostctl cue-exec --execute examples/recipe/all_hosts.cue kafka
recipe: {
	name: "all-in-current-search"
	steps: [
		{host: "*", command: "hostname"},
		{host: "*", command: "uptime"},
	]
}
