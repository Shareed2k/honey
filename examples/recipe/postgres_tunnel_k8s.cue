// Kubernetes pod port-forward tunnel for Postgres (operator-side pgx).
//
// Target host must be a k8s pod reference (provider k8s). Honey uses the k8s API
// port-forward path instead of SSH -L.
//
// Plan:
//   honey cue-exec examples/recipe/postgres_tunnel_k8s.cue "k8s:my-postgres-pod"
// Execute:
//   honey cue-exec --execute examples/recipe/postgres_tunnel_k8s.cue "k8s:my-postgres-pod"

recipe: {
	name: "postgres-tunnel-k8s"
	type: "graph"

	defaults: {
		secrets: {
			PG_DSN: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"
		}
	}

	steps: [
		{
			id:   "pg_tunnel"
			host: "k8s:my-postgres-pod"
			tunnel: {
				remote_port: 5432
			}
		},
		{
			id:      "pg_query"
			host:    "k8s:my-postgres-pod"
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
		}
	]
}
