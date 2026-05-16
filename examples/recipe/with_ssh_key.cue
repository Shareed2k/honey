// Recipe SSH private key example — dry-run: honey cue-exec examples/recipe/with_ssh_key.cue "*"
recipe: {
	name: "with-key"
	defaults: {
		ssh_private_key: "~/.ssh/my_staging_key"
	}
	steps: [{
		host:    "*"
		command: "hostname"
	}, {
		host:            "*"
		ssh_private_key: "~/.ssh/other_key" // overrides defaults for this step only
		command:         "whoami"
	}]
}
