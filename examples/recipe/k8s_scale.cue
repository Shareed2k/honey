// Graph recipe: scale a deployment to 0, wait for rollout, then scale back up.
//
// Useful for a forced pod recycle (e.g. force secret rotation pickup).
// Targets k8s host records (provider == k8s).
//
// Validate:
//   honey cue-validate examples/recipe/k8s_scale.cue
// Plan:
//   honey cue-exec examples/recipe/k8s_scale.cue "re:provider==k8s"
// Run:
//   honey cue-exec examples/recipe/k8s_scale.cue "re:provider==k8s" --execute
recipe: {
	name: "k8s-scale"
	type: "graph"
	steps: [
		{
			id:   "scale_down"
			host: "re:provider==k8s"
			k8s: {
				namespace: "production"
				scale: {
					resource: "deployment/worker"
					replicas: 0
				}
			}
		},
		{
			id:      "wait_down"
			host:    "re:provider==k8s"
			depends: ["scale_down"]
			k8s: {
				namespace: "production"
				wait: {
					resource: "deployment/worker"
					"for":    "jsonpath=.status.availableReplicas=0"
					timeout:  "60s"
				}
			}
		},
		{
			id:      "scale_up"
			host:    "re:provider==k8s"
			depends: ["wait_down"]
			k8s: {
				namespace: "production"
				scale: {
					resource: "deployment/worker"
					replicas: 3
				}
			}
		},
		{
			id:      "wait_up"
			host:    "re:provider==k8s"
			depends: ["scale_up"]
			k8s: {
				namespace: "production"
				wait: {
					resource: "deployment/worker"
					"for":    "condition=Available"
					timeout:  "120s"
				}
			}
		},
	]
}
