// Graph recipe: rolling restart Kafka controllers (Ansible restart_kafka.yml controllers path).
//
// Flow: quorum-cli list-nodes → pre-verify on first controller → per controller (serial):
//   service plugin restart → quorum-cli am-i-caught-up verify (with step retry).
//
// Uses CUE for-loop over injected search hosts (one restart+verify pair per controller).
// honey cue-validate does NOT work (reference "hosts" not found); use cue-exec dry-run instead.
//
// Install service plugin:
//   make build-plugin-modules
//   mkdir -p ~/.config/honey/plugins/service
//   cp examples/plugins/service/plugin.yaml examples/plugins/service/plugin.wasm ~/.config/honey/plugins/service/
//
// Requires plugins.enabled in honey config, quorum-cli on controllers, passwordless root for restart steps.
//
// Search should narrow to one cluster's controllers; order should match quorum-cli list-nodes output.
// Plan:
//   honey cue-exec examples/recipe/kafka_controller_rolling_restart.cue "re:.*my-cluster.*controller.*"
// Run:
//   honey cue-exec --execute examples/recipe/kafka_controller_rolling_restart.cue "re:.*my-cluster.*controller.*"

import "list"

_verifyCmd: """
set -euo pipefail
timeout="${KAFKA_CONTROLLER_VERIFY_TIMEOUT:-30s}"
quorum-cli am-i-caught-up --timeout="$timeout"
"""

_verifyRetry: {
	attempts:  30
	delay_ms:  2000
	backoff:   "fixed"
}

recipe: {
	name: "kafka-controller-rolling-restart"
	type: "graph"

	defaults: {
		run_as: "ubuntu"
		env: {
			KAFKA_SERVICE_NAME:              "kafka"
			KAFKA_CLUSTER_NAME:              "my-cluster"
			KAFKA_CONTROLLER_VERIFY_TIMEOUT: "30s"
		}
	}

	steps: list.Concat([
		[
			{
				id:   "list_nodes"
				host: hosts[0].name
				command: "uorum-cli list-nodes"
			},
			{
				id:      "pre_verify"
				host:    hosts[0].name
				depends: ["list_nodes"]
				command: _verifyCmd
				retry:   _verifyRetry
			},
		],
		for i in list.Range(0, len(hosts), 1) {
			let h = hosts[i]
			[
				{
					id:   "restart_\(i)"
					host: h.name
					depends: [
						if i == 0 {"pre_verify"},
						if i > 0 {"verify_\(i-1)"},
					]
					run_as: "root"
					plugin: {
						id:     "service"
						action: "manage"
						config: {
							name:  "${KAFKA_SERVICE_NAME}"
							state: "restarted"
						}
					}
				},
				{
					id:      "verify_\(i)"
					host:    h.name
					depends: ["restart_\(i)"]
					command: _verifyCmd
					retry:   _verifyRetry
				},
			]
		},
	])
}
