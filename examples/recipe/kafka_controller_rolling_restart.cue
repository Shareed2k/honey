// examples/recipe/kafka_controller_rolling_restart.cue
// Demonstrates dynamic rolling restart of Kafka controllers using quorum-cli.
// The active controller is detected and restarted last.
//
// Usage:
// honey cue-exec examples/recipe/kafka_controller_rolling_restart.cue 're:^kafka-.*' --execute

_healthCheck: {
	command: "quorum-cli am-i-caught-up"
}

recipe: {
	name: "kafka-controller-rolling-restart"
	type: "graph"
	defaults: {
		max_parallel: 1
		run_as:       "root"
	}
	steps: [
		{
			id: "list_nodes"
			host: "*"
			command: "quorum-cli list-nodes"
			output:  "controllers_raw"
		},
        _healthCheck & {
			id:      "verify_cluster_health"
			host:    "*"
			depends: ["list_nodes"]
		},
		{
			id: "restart"
			depends: ["verify_cluster_health"]
			host: "${item}"
			serial:  1
			loop: "{{ .outputs.controllers_raw.stdout_lines | compact | toJson }}"
			command: "systemctl restart kafka.service"
			hooks: {
				on_success: _healthCheck
			}
		},
	]
}
