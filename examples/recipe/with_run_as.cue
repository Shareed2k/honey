// defaults.run_as applies to every step unless the step sets its own run_as.
// Remote wrap: sudo -n -u '<user>' -- sh -lc '...' (requires passwordless sudo).
recipe: {
	name: "commands-as-unprivileged-user"
	defaults: {run_as: "nobody"}
	steps: [
		{host: "*", command: "id -un"},
		{host: "*", command: "id -un", run_as: "root"},
		{host: "*", command: "uptime", run_as: "root"},
	]
}
