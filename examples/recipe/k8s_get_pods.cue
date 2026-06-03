// Graph recipe: get pods by label selector, capture JSON output, render a summary template.
//
// The "get_pods" step captures its stdout into the graph env via output + env_from.
// The downstream template step formats a brief pod list from the captured JSON.
// Targets k8s host records (provider == k8s).
//
// Validate:
//   honey cue-validate examples/recipe/k8s_get_pods.cue
// Plan:
//   honey cue-exec examples/recipe/k8s_get_pods.cue "re:provider==k8s"
// Run:
//   honey cue-exec examples/recipe/k8s_get_pods.cue "re:provider==k8s" --execute
recipe: {
	name: "k8s-get-pods"
	type: "graph"
	steps: [
		{
			id:   "get_pods"
			host: "re:provider==k8s"
			k8s: {
				namespace:      "production"
				output:         "pods_json"
				get: {
					resource:       "pods"
					label_selector: "app=api"
					format:         "json"
				}
			}
		},
		{
			id:      "summarize"
			host:    "_"
			depends: ["get_pods"]
			env_from: [{
				step: "get_pods"
				map: PODS_JSON: "stdout"
			}]
			template: {
				template: "Pods snapshot:\n{{ .PODS_JSON }}\n"
				data:     {}
			}
		},
	]
}
