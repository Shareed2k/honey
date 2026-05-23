// Postgres over SSH local forward: operator-side tunnel + host-mediated pgx query.
//
// Postgres listens on loopback on the remote host (typical for managed DB agents).
// The tunnel step opens 127.0.0.1:<local_port> on the operator; the postgres plugin
// rewrites the sealed DSN to that endpoint via tunnel_step.
//
// Install:
//   make build-plugin-modules
//   mkdir -p ~/.config/honey/plugins/postgres
//   cp examples/plugins/postgres/plugin.yaml examples/plugins/postgres/plugin.wasm ~/.config/honey/plugins/postgres/
//
// Seal a DSN pointing at the remote loopback address (host in DSN is rewritten at runtime):
//   echo -n 'postgres://user:pass@localhost:5432/app?sslmode=require' | honey secrets seal --config ~/.config/honey/config.yaml
//
// Plan:
//   honey cue-exec examples/recipe/postgres_tunnel_demo.cue "db-*"
// Run:
//   honey cue-exec --execute examples/recipe/postgres_tunnel_demo.cue "db-*"
//
// share_key reuses the same operator listen port across unrelated cue-exec runs.
// See also: postgres_tunnel_ssh_config.cue, postgres_tunnel_k8s.cue
recipe: {
	name: "postgres-tunnel-demo"
	type: "graph"

	defaults: {
		secrets: {
			// Replace with: honey secrets seal ...
			PG_DSN: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"
		}
	}

	steps: [
		{
			id:   "pg_tunnel"
			host: "db-*"
			tunnel: {
				mode:        "local"
				remote_host: "localhost"
				remote_port: 5432
				share_key:   "db-primary-pg"
			}
		},
		{
			id:      "pg_query"
			host:    "db-*"
			depends: ["pg_tunnel"]
			plugin: {
				id:     "postgres"
				action: "query"
				config: {
					dsn_secret:  "PG_DSN"
					tunnel_step: "pg_tunnel"
					timeout_ms:  10000
					readonly:    true
					sql: """
						SELECT now() AS ts, count(*)::int AS n FROM pg_stat_activity;
					"""
					params: []
				}
			}
		},
	]
}
