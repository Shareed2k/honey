// Postgres tunnel using ssh_config LocalForward (Match exec via ssh_config_env).
//
// Requires ~/.ssh/config entries such as:
//   Match exec "test \"$ROLE\" = prod"
//     LocalForward 15432 localhost:5432
//
// Plan:
//   honey cue-exec examples/recipe/postgres_tunnel_ssh_config.cue "db-primary"
//
// ssh_config_env is passed to `ssh -G` so Match exec predicates see the same env.
recipe: {
	name: "postgres-tunnel-ssh-config"
	type: "graph"

	defaults: {
		secrets: {
			PG_DSN: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"
		}
	}

	steps: [
		{
			id:   "pg_tunnel"
			host: "db-primary"
			tunnel: {
				use_ssh_config:   true
				ssh_config_match: "5432"
				ssh_config_env: {
					ROLE: "prod"
				}
			}
		},
		{
			id:      "pg_query"
			host:    "db-primary"
			depends: ["pg_tunnel"]
			plugin: {
				id:     "postgres"
				action: "query"
				config: {
					dsn_secret:  "PG_DSN"
					tunnel_step: "pg_tunnel"
					readonly:    true
					sql:         "SELECT 1 AS ok"
					params:      []
				}
			}
		},
	]
}
