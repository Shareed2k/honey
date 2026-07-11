// Remote recipe: read-only Postgres diagnostics for replica lag triage.
//
// Steps (all use host: "*"; narrow with honey search / re:.*replica.* / single host):
//  1) Standby replay lag vs now(), plus pg_stat_replication rows when this node is primary.
//  2) Sessions running longer than 5 minutes (pg_stat_activity).
//  3) Non-interactive postgres process snapshot (pgrep + ps aux).
//  4) Top postgres/postmaster lines by CPU% via GNU ps --sort; map client IPs in argv
//     to instances (honey search). Optional: htop manually if you prefer a live view.
//
// Secrets: do NOT put PGPASSWORD in this file. Pass at run time, e.g.:
//   honey cue-exec -e PGPASSWORD='...' examples/recipe/postgres_replica_lag.cue "replica" --execute
// Optional: -e PGHOST=... if not local; tune PGUSER / PGDATABASE / PGPORT via defaults or -e.
// psql uses -w (no password prompt): without credentials the DB steps print skip and exit 0 so
// OS process steps still run; do not rely on a hung "Password for user postgres:" prompt.
//
// Killing sessions is intentionally NOT included. Use PgHero or psql/pg_terminate_backend
// manually after you identify the backend. If a worker looks like Redash, confirm before kill.
//
// Validate:
//   honey cue-validate examples/recipe/postgres_replica_lag.cue
// Plan:
//   honey cue-exec examples/recipe/postgres_replica_lag.cue "<search>"
// Run:
//   honey cue-exec examples/recipe/postgres_replica_lag.cue "<search>" --execute
recipe: {
	name: "postgres-replica-lag-triage"

	defaults: {
		//env: {
		//	PGHOST:     "127.0.0.1"
		//	PGPORT:     "5432"
		//	PGUSER:     "postgres"
		//	PGDATABASE: "postgres"
		//}
		run_as: "postgres"
	}

	steps: [
		{
			host: "*"
			command: """
echo "=== $HONEY_HOST_NAME ($HONEY_HOST_PRIMARY_IP) postgres replication ==="
if ! command -v psql >/dev/null 2>&1; then echo "skip: psql not in PATH"; exit 0; fi
if ! psql -w -v ON_ERROR_STOP=0 <<'EOSQL'
SELECT pg_is_in_recovery() AS standby;
SELECT CASE WHEN pg_is_in_recovery()
  THEN EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))
  END AS replay_lag_seconds;
SELECT application_name, client_addr::text, state, sync_state
FROM pg_stat_replication
LIMIT 15;
EOSQL
then
	echo "skip: psql failed (auth or connection). Pass -e PGPASSWORD=... / fix PGUSER; OS process steps still run below."
	exit 0
fi
echo "(hint) See later steps for pgrep/ps and CPU-sorted ps. Map client IPs to services (e.g. Redash)."
"""
		},
		{
			host: "*"
			command: """
echo "=== $HONEY_HOST_NAME sessions over 5 minutes ==="
if ! command -v psql >/dev/null 2>&1; then echo "skip: psql not in PATH"; exit 0; fi
if ! psql -w -v ON_ERROR_STOP=0 <<'EOSQL'
SELECT
  pid,
  now() - pg_stat_activity.query_start AS duration,
  query,
  state
FROM pg_stat_activity
WHERE (now() - pg_stat_activity.query_start) > interval '5 minutes';
EOSQL
then
	echo "skip: psql failed (auth or connection). Pass -e PGPASSWORD=... / fix PGUSER."
	exit 0
fi
"""
		},
		{
			host: "*"
			command: "echo \"=== $HONEY_HOST_NAME postgres-related processes (snapshot) ===\" && (command -v pgrep >/dev/null 2>&1 && { pgrep -af '^postgres:' 2>/dev/null; pgrep -af 'bin/postgres ' 2>/dev/null; } | sort -u | head -40 || true) && (ps auxww 2>/dev/null | grep -vF 'HONEY_HOST_NAME' | grep -E ' [p]ostgres: |/[b]in/postgres |/usr/lib/postgresql/.*/bin/postgres ' | head -35 || true)"
		},
		{
			host: "*"
			command: "echo \"=== $HONEY_HOST_NAME postgres by CPU% (GNU ps) ===\" && (ps -eo pid,user,%cpu,%mem,etime,args --sort=-%cpu 2>/dev/null | { head -n1; grep -vF 'HONEY_HOST_NAME' | grep -iE 'postgres:|postmaster|/[b]in/postgres |/usr/lib/postgresql/.*/bin/postgres ' | head -25; } || echo \"skip: ps --sort not supported (non-GNU ps?)\")"
		},
		{
			host: "_"
			notify: {
				notify_subject: "Honey AI summary"
				services: { http: {} }
			}
			summarize: {
				prompt: """
Summarize the host listing in 3–5 bullet points. Note any missing output or failures.
"""
				model: "models/gemini-3.1-pro-preview"
				// max_input_chars: 100000
				// max_output_tokens: 800
			}
		},
	]
}
