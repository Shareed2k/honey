// Graph recipe: postgres query → KV extract → chained query via env_from.kv + ${VAR} params.
//
// Requires postgres plugin installed and a sealed PG_DSN in defaults.secrets.
//
//   make build-plugin-modules
//   honey cue-validate examples/recipe/postgres_kv_demo.cue
//   honey cue-exec examples/recipe/postgres_kv_demo.cue "db-*"
//   honey cue-exec --execute examples/recipe/postgres_kv_demo.cue "db-*"
//
// See also: examples/recipe/postgres_module_demo.cue
recipe: {
	name: "postgres-kv-demo"
	type: "graph"

	defaults: {
		secrets: {
			PG_DSN: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"
		}
	}

	steps: [
		{
			id:   "pg_query"
			host: "db-*"
			plugin: {
				id:     "postgres"
				action: "query"
				config: {
					dsn_secret: "PG_DSN"
					timeout_ms: 10000
					readonly:   true
					kv_key:     "pg_activity"
					kv_key_per_host: true
					extract: {
						count: ".[0].n"
					}
					sql: """
						SELECT count(*)::int AS n FROM pg_stat_activity;
					"""
					params: []
				}
			}
		},
		{
			id:      "pg_followup"
			host:    "db-*"
			depends: ["pg_query"]
			env_from: [{
				kv: { THRESHOLD: "pg_activity_db-primary_count" }
			}]
			plugin: {
				id:     "postgres"
				action: "query"
				config: {
					dsn_secret: "PG_DSN"
					timeout_ms: 10000
					readonly:   true
					sql: """
						SELECT usename FROM pg_stat_activity
						WHERE state = 'active' AND $1::int >= 0
						LIMIT 5;
					"""
					params: ["${THRESHOLD}"]
				}
			}
		},
		{
			id:      "render"
			host:    "_"
			depends: ["pg_query"]
			template: {
				template: "count={{ kvGet \"pg_activity_db-primary_count\" | default \"0\" }}\n"
				output: "RESULT"
			}
		},
	]
}
