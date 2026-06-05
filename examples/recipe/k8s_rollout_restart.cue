// Rolling restart a Kubernetes deployment and wait for rollout completion.
//
// Targets k8s host records (provider == k8s). Context and default namespace
// come from the host meta; namespace field overrides when set.
//
// Validate:
//   honey cue-validate examples/recipe/k8s_rollout_restart.cue
// Plan:
//   honey cue-exec examples/recipe/k8s_rollout_restart.cue "re:provider==k8s"
// Run:
//   honey cue-exec examples/recipe/k8s_rollout_restart.cue "re:provider==k8s" --execute
recipe: {
	name: "k8s-rollout-restart"
	steps: [
		{
			host: "re:provider==k8s"
			k8s: {
				namespace: "production"
				rollout_restart: {
					resource: "deployment/api"
					wait:     true
				}
			}
		},
	]
}
