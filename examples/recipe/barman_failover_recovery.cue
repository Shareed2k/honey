// Graph recipe: recover Barman after PostgreSQL failover (PostgreSQLBarmanErrors alert).
//
// After failover, stale pg_receivewal can block WAL streaming. This recipe:
//  1) barman check (baseline)
//  2) pkill pg_receivewal
//  3) wait 30s
//  4) barman check (expect OK)
//  5) restart barman-exporter via service plugin (clears exporter cache)
//  6) optional receive-wal --reset when WAL/slot desync persists (when + -e BARMAN_DO_RESET=true)
//
// Default path fixes most post-failover alerts. Escalate when logs show timeline fork
// or "requested WAL segment … has already been removed":
//   honey cue-exec -e BARMAN_DO_RESET=true … --execute
//
// Install service plugin:
//   make build-plugin-modules
//   mkdir -p ~/.config/honey/plugins/service
//   cp examples/plugins/service/plugin.yaml examples/plugins/service/plugin.wasm ~/.config/honey/plugins/service/
//
// Requires plugins.enabled in honey config, SSH run_as barman (defaults), passwordless root for restart_exporter step.
// Override server / exporter unit at run time:
//   honey cue-exec -e BARMAN_SERVER=postgres-main \
//     -e BARMAN_EXPORTER_SERVICE=barman-exporter \
//     examples/recipe/barman_failover_recovery.cue "barman-*" --execute
//
// Validate:
//   honey cue-validate examples/recipe/barman_failover_recovery.cue
// Plan:
//   honey cue-exec examples/recipe/barman_failover_recovery.cue "barman-*"
// Run:
//   honey cue-exec --execute examples/recipe/barman_failover_recovery.cue "barman-*"
// Debug:
//   honey cue-exec --debug-log=/dev/stderr examples/recipe/barman_failover_recovery.cue "barman-*"
recipe: {
	name: "barman-failover-recovery"
	type: "graph"

	defaults: {
		run_as: "barman"
		env: {
			BARMAN_SERVER:           "postgres-main"
			BARMAN_EXPORTER_SERVICE: "barman-exporter"
			BARMAN_DO_RESET:         "false"
		}
	}

	steps: [
		{
			id:   "pre_check"
			host: "*"
			command: "barman check $BARMAN_SERVER"
		},
		{
			id:      "kill_receivewal"
			host:    "*"
			depends: ["pre_check"]
			command: """
				pkill -f pg_receivewal || echo "no pg_receivewal process found"
				pgrep -af pg_receivewal 2>/dev/null || echo "(no pg_receivewal processes remaining)"
			"""
		},
		{
			id:      "wait_30s"
			host:    "*"
			depends: ["kill_receivewal"]
			command: "sleep 30"
		},
		{
			id:      "post_check"
			host:    "*"
			depends: ["wait_30s"]
			command: "barman check $BARMAN_SERVER"
		},
		{
			id:      "restart_exporter"
			host:    "*"
			depends: ["post_check"]
			run_as:  "root"
			plugin: {
				id:     "service"
				action: "manage"
				config: {
					name:  "${BARMAN_EXPORTER_SERVICE}"
					state: "restarted"
				}
			}
		},
		{
			id:      "reset_receivewal"
			host:    "*"
			depends: ["restart_exporter"]
			when:    "env['BARMAN_DO_RESET'] == 'true'"
			command: """
				barman receive-wal --reset "$BARMAN_SERVER"
				barman check "$BARMAN_SERVER"
			"""
		},
	]
}
