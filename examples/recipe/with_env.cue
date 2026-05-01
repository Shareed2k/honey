// Optional env on command/script steps (POSIX names; values single-quoted on the remote).
// defaults.env merges with each step's env; duplicate keys use the step value.
//   honey cue-validate examples/recipe/with_env.cue
//   honey cue-exec examples/recipe/with_env.cue my-filter
recipe: {
	name: "with-env"
	defaults: {
		env: {
			GLOBAL_MSG: "from defaults"
		}
	}
	steps: [
		{host: "*", command: "echo \"$GLOBAL_MSG\""},
		{
			host:    "*"
			command: "printenv FOO"
			env: {
				STEP_ONLY: "step"
				FOO:       "bar"
			}
		},
	]
}
