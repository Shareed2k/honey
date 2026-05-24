// Example recipe using the postgres WASM module (host-mediated pgx on the operator).
//
// DSN is resolved only in Honey core from defaults.secrets (secure:v1 refs).
// WASM never receives credentials or opens network connections.
//
// Install:
//   make build-plugin-modules
//   mkdir -p ~/.config/honey/plugins/postgres
//   cp examples/plugins/postgres/plugin.yaml examples/plugins/postgres/plugin.wasm ~/.config/honey/plugins/postgres/
//
// Seal a DSN (connection string) and paste into defaults.secrets.PG_DSN:
//   echo -n 'postgres://user:pass@host:5432/app?sslmode=require' | honey secrets seal --config ~/.config/honey/config.yaml
//
//   honey cue-exec examples/recipe/postgres_module_demo.cue "db-*"
//   honey cue-exec --execute examples/recipe/postgres_module_demo.cue "db-*"
//
// Chained queries with KV + jq: see postgres_kv_demo.cue
recipe: {
	name: "postgres-module-demo"

	defaults: {
		secrets: {
			// Replace with output of: honey secrets seal ...
			PG_DSN: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"
		}
	}

	steps: [{
		host: "db-*"
		plugin: {
			id: "postgres"
			action: "query"
			config: {
				dsn_secret: "PG_DSN"
				timeout_ms: 10000
				readonly:   true
				sql: """
					SELECT now() AS ts, count(*)::int AS n FROM pg_stat_activity;
				"""
				params: []
			}
		}
	}]
}
