// Example remote recipe — see examples/recipe/README.md for more samples (all_hosts, by_regex, …).
// Validate: honey cue-validate examples/recipe/recipe.cue
// Dry-run: honey cue-exec examples/recipe/recipe.cue my-host-prefix
// SSH:     honey cue-exec --execute … (same search flags as honey search).
recipe: {
	name: "example-check"
	// Optional: wrap every step's command as sudo -n -u <user> -- sh -lc '...' on the remote.
	// defaults: { run_as: "nginx" }
	steps: [
		// host can be: a literal IP; exact name (one row); "*" for all with IP; or "re:^kafka-.+$"
		// (Go regexp on instance name, RE2; add (?i) for case-insensitive).
		{host: "10.0.0.1", command: "uname -a"},
		{host: "10.0.0.2", command: "test -f /etc/os-release && cat /etc/os-release"},
		// Example: ./honey cue-exec --execute this_file.cue kafka  → runs on all "kafka" rows with IPs
		// {host: "*", command: "hostname"},
		// Per-step run_as overrides defaults.run_as when both are set.
		// {host: "my-vm-name", command: "id", run_as: "nobody"},
		// Optional env (command/script only): defaults.env + step.env, exports before the shell.
		// {host: "10.0.0.1", command: "printenv MY_VAR", env: {MY_VAR: "hello"}},
	]
}
