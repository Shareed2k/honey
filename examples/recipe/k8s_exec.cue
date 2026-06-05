// Graph recipe: exec a command in a running pod and capture the output.
//
// Runs `df -h /data` in the api container of the specified pod. The captured
// stdout is available to downstream steps via env_from. Targets k8s host
// records (provider == k8s).
//
// Validate:
//   honey cue-validate examples/recipe/k8s_exec.cue
// Plan:
//   honey cue-exec examples/recipe/k8s_exec.cue "re:provider==k8s"
// Run:
//   honey cue-exec examples/recipe/k8s_exec.cue "re:provider==k8s" --execute
recipe: {
	name: "k8s-exec"
	type: "graph"
	steps: [
		{
			id:   "disk_check"
			host: "re:provider==k8s"
			k8s: {
				namespace: "production"
				output:    "disk_usage"
				exec: {
					pod:       "api-7d6f8b9c4-xk2pq"
					container: "api"
					command: ["df", "-h", "/data"]
				}
			}
		},
		{
			id:      "report"
			host:    "_"
			depends: ["disk_check"]
			env_from: [{
				step: "disk_check"
				map: DISK_USAGE: "stdout"
			}]
			template: {
				template: "Disk usage on api pod:\n{{ .DISK_USAGE }}\n"
				data:     {}
			}
		},
	]
}
