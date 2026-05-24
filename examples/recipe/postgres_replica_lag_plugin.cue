// Graph recipe: replica lag triage with tunnel + postgres + bash WASM plugins (operator-side pgx).
//
// Same intent as postgres_replica_lag.cue (repl lag, long sessions, OS process snapshot, CPU ps, AI summary)
// but uses sealed PG_DSN + SSH tunnel + plugins instead of remote psql and -e PGPASSWORD.
//
// Legacy alternative (remote psql): examples/recipe/postgres_replica_lag.cue
//
// Install:
//   make build-plugin-modules
//   for m in postgres bash; do
//     mkdir -p ~/.config/honey/plugins/$m
//     cp examples/plugins/$m/plugin.yaml examples/plugins/$m/plugin.wasm ~/.config/honey/plugins/$m/
//   done
//
// Seal DSN (localhost in DSN; tunnel_step rewrites host/port on the operator):
//   echo -n 'postgres://user:pass@localhost:5432/postgres?sslmode=require' | honey secrets seal --config ~/.config/honey/config.yaml
//
// Validate:
//   honey cue-validate examples/recipe/postgres_replica_lag_plugin.cue
// Plan:
//   honey cue-exec examples/recipe/postgres_replica_lag_plugin.cue "replica-*"
// Run:
//   honey cue-exec --execute examples/recipe/postgres_replica_lag_plugin.cue "replica-*"
// Debug:
//   honey cue-exec --debug-log=/dev/stderr examples/recipe/postgres_replica_lag_plugin.cue "replica-*"
//
// Killing sessions is NOT included. Map client IPs from repl_clients / process output manually.
recipe: {
	name: "postgres-replica-lag-triage-plugin"
	type: "graph"

	defaults: {
		run_as: "postgres"
		secrets: {
			// Replace with: honey secrets seal ...
			PG_DSN: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"
		}
	}

	steps: [
		{
			id:   "pg_tunnel"
			host: "*"
			tunnel: {
				mode:        "local"
				remote_host: "localhost"
				remote_port: 5432
				share_key:   "replica-pg"
			}
		},
		{
			id:      "repl_lag"
			host:    "*"
			depends: ["pg_tunnel"]
			plugin: {
				id:     "postgres"
				action: "query"
				config: {
					dsn_secret:  "PG_DSN"
					tunnel_step: "pg_tunnel"
					timeout_ms:  15000
					readonly:    true
					sql: """
						SELECT
						  pg_is_in_recovery() AS standby,
						  CASE WHEN pg_is_in_recovery()
						    THEN EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))
						  END AS replay_lag_seconds
					"""
					params: []
				}
			}
		},
		{
			id:      "repl_clients"
			host:    "*"
			depends: ["pg_tunnel"]
			plugin: {
				id:     "postgres"
				action: "query"
				config: {
					dsn_secret:  "PG_DSN"
					tunnel_step: "pg_tunnel"
					timeout_ms:  15000
					readonly:    true
					sql: """
						SELECT application_name, client_addr::text, state, sync_state
						FROM pg_stat_replication
						LIMIT 15
					"""
					params: []
				}
			}
		},
		{
			id:      "long_sessions"
			host:    "*"
			depends: ["pg_tunnel"]
			plugin: {
				id:     "postgres"
				action: "query"
				config: {
					dsn_secret:  "PG_DSN"
					tunnel_step: "pg_tunnel"
					timeout_ms:  15000
					readonly:    true
					sql: """
						SELECT
						  pid,
						  now() - pg_stat_activity.query_start AS duration,
						  query,
						  state
						FROM pg_stat_activity
						WHERE (now() - pg_stat_activity.query_start) > interval '5 minutes'
					"""
					params: []
				}
			}
		},
		{
			id:      "proc_snapshot"
			host:    "*"
			depends: ["pg_tunnel"]
			plugin: {
				id:     "bash"
				action: "run"
				config: {
					script: """
						set -euo pipefail
						echo "=== ${HONEY_HOST_NAME} (${HONEY_HOST_PRIMARY_IP}) postgres-related processes (snapshot) ==="
						if command -v pgrep >/dev/null 2>&1; then
						  { pgrep -af '^postgres:' 2>/dev/null; pgrep -af 'bin/postgres ' 2>/dev/null; } | sort -u | head -40 || true
						fi
						ps auxww 2>/dev/null | grep -vF 'HONEY_HOST_NAME' | grep -E ' [p]ostgres: |/[b]in/postgres |/usr/lib/postgresql/.*/bin/postgres ' | head -35 || true
					"""
				}
			}
		},
		{
			id:      "cpu_ps"
			host:    "*"
			depends: ["pg_tunnel"]
			plugin: {
				id:     "bash"
				action: "run"
				config: {
					script: """
						set -euo pipefail
						echo "=== ${HONEY_HOST_NAME} postgres by CPU% (GNU ps) ==="
						ps -eo pid,user,%cpu,%mem,etime,args --sort=-%cpu 2>/dev/null | {
						  head -n1
						  grep -vF 'HONEY_HOST_NAME' | grep -iE 'postgres:|postmaster|/[b]in/postgres |/usr/lib/postgresql/.*/bin/postgres ' | head -25
						} || echo "skip: ps --sort not supported (non-GNU ps?)"
					"""
				}
			}
		},
		{
			id:   "summarize"
			host: "_"
			depends: [
				"repl_lag",
				"repl_clients",
				"long_sessions",
				"proc_snapshot",
				"cpu_ps",
			]
			notify: {
				notify_subject: "Honey AI summary"
				services: { http: {} }
			}
			ai: {
				prompt: """
Summarize the host listing in 3–5 bullet points. Note any missing output or failures.
"""
				model: "models/gemini-3.1-pro-preview"
			}
		},
	]
}
