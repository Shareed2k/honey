// One step: SFTP upload then `sh <remote>` on the same SSH session per host.
// Optional run_as wraps the run with sudo -n (upload always as SSH user).
//
//   hostctl cue-validate examples/recipe/script_step.cue
//   hostctl cue-exec examples/recipe/script_step.cue my-filter
recipe: {
	name: "upload-and-run"
	steps: [
		{
			host: "*"
			script: {
				local:  "./hello.sh"
				remote: "/tmp/hostctl-hello.sh"
			}
		},
	]
}
