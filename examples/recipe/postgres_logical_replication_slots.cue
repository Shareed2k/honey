// Remote recipe: read-only Postgres diagnostics for logical replication slots and publications.
//
// Steps (all use host: "*"; narrow with honey search, e.g. re:.*postgres.*, or a single host):
//  1) Standby flag + pg_replication_slots (listing / bloat triage).
//  2) pg_publication (+ sample of pg_publication_tables, capped).
//  3) pg_stat_activity rows whose query mentions pg_replication_slot_advance (replica WAL dir / advance failures).
//  4) Primary-only WAL distance vs restart_lsn / confirmed_flush_lsn (NULL on standbys).
//
// Runbook context (manual; not executed by this file):
//  - Alerts: PostgreSQLReplicationSlotsBloat, PostgreSQLInactiveReplicationSlot, PostgreSQLPgWALDirectoryBloat.
//  - Grafana: compare slot/WAL spike pattern vs baseline.
//  - Wazuh Kibana: message: "Failed to advance logical replication slot" (timeouts on replica advance).
//  - Client-side: Debezium / logical consumers — errors, disconnects, inactive slots retaining WAL.
//  - Inactive slot no longer needed: stop the consumer, then drop slot + publication manually or data loss risk persists.
//
// NOT in this recipe (destructive or site-specific — do by hand after change control):
//  - SELECT pg_catalog.pg_replication_slot_advance('slot', 'lsn') ;
//  - SELECT pg_drop_replication_slot('slot_name');   -- not "SELECT * FROM pg_drop_replication_slot(...)"
//  - DROP PUBLICATION pub_name;
//  - Patroni: patronictl edit-config (remove slot from config).
//  - RDS vs self-managed: follow your platform runbook for slot drops.
//
// Secrets: do NOT put PGPASSWORD in this file. Pass at run time, e.g.:
//   honey cue-exec -e PGPASSWORD='...' examples/recipe/postgres_logical_replication_slots.cue "postgres" --execute
// Optional: -e PGHOST=... ; tune PGUSER / PGDATABASE / PGPORT via defaults or -e. Host .pgpass works with psql -w.
// psql uses -w (no password prompt): without credentials, steps print skip and exit 0.
//
// Validate:
//   honey cue-validate examples/recipe/postgres_logical_replication_slots.cue
// Plan:
//   honey cue-exec examples/recipe/postgres_logical_replication_slots.cue "<search>"
// Run:
//   honey cue-exec examples/recipe/postgres_logical_replication_slots.cue "<search>" --execute
recipe: {
	name: "postgres-logical-replication-slots-triage"

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
echo "=== $HONEY_HOST_NAME ($HONEY_HOST_PRIMARY_IP) replication slots ==="
if ! command -v psql >/dev/null 2>&1; then echo "skip: psql not in PATH"; exit 0; fi
if ! psql -w -v ON_ERROR_STOP=0 <<'EOSQL'
SELECT pg_is_in_recovery() AS standby;
SELECT * FROM pg_replication_slots;
EOSQL
then
	echo "skip: psql failed (auth or connection). Pass -e PGPASSWORD=... / fix PGUSER."
	exit 0
fi
"""
		},
		{
			host: "*"
			command: """
echo "=== $HONEY_HOST_NAME ($HONEY_HOST_PRIMARY_IP) publications ==="
if ! command -v psql >/dev/null 2>&1; then echo "skip: psql not in PATH"; exit 0; fi
if ! psql -w -v ON_ERROR_STOP=0 <<'EOSQL'
SELECT * FROM pg_publication;
SELECT * FROM pg_publication_tables LIMIT 100;
EOSQL
then
	echo "skip: psql failed (auth or connection). Pass -e PGPASSWORD=... / fix PGUSER."
	exit 0
fi
"""
		},
		{
			host: "*"
			command: """
echo "=== $HONEY_HOST_NAME ($HONEY_HOST_PRIMARY_IP) pg_replication_slot_advance in pg_stat_activity ==="
if ! command -v psql >/dev/null 2>&1; then echo "skip: psql not in PATH"; exit 0; fi
if ! psql -w -v ON_ERROR_STOP=0 <<'EOSQL'
SELECT pid, usename, state, query_start, query
FROM pg_stat_activity
WHERE query LIKE '%pg_replication_slot_advance%';
EOSQL
then
	echo "skip: psql failed (auth or connection). Pass -e PGPASSWORD=... / fix PGUSER."
	exit 0
fi
"""
		},
		{
			host: "*"
			command: """
echo "=== $HONEY_HOST_NAME ($HONEY_HOST_PRIMARY_IP) slot WAL distance (primary only; NULL on standby) ==="
if ! command -v psql >/dev/null 2>&1; then echo "skip: psql not in PATH"; exit 0; fi
if ! psql -w -v ON_ERROR_STOP=0 <<'EOSQL'
SELECT
  slot_name,
  slot_type,
  database,
  active,
  restart_lsn,
  confirmed_flush_lsn,
  CASE
    WHEN NOT pg_is_in_recovery() AND restart_lsn IS NOT NULL
      THEN pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn))
  END AS wal_pretty_behind_restart_lsn,
  CASE
    WHEN NOT pg_is_in_recovery() AND confirmed_flush_lsn IS NOT NULL
      THEN pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn))
  END AS wal_pretty_behind_confirmed_flush_lsn
FROM pg_replication_slots;
EOSQL
then
	echo "skip: psql failed (auth or connection). Pass -e PGPASSWORD=... / fix PGUSER."
	exit 0
fi
"""
		},
	]
}
