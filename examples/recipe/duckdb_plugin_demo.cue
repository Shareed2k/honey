// Demo recipe for the duckdb Docker plugin.
//
//   honey cue-exec --execute examples/recipe/duckdb_plugin_demo.cue
//
// Requires plugins.enabled: true and the duckdb plugin installed. Steps
// target host: "_" (local-only) — runtime: docker plugins always execute in
// a container on the operator machine and never touch a target host, so no
// search backend or matching host record is needed at all.
recipe: {
	name: "duckdb-plugin-demo"
	steps: [
		{
			host: "_"
			plugin: {
				id:     "duckdb"
				action: "query"
				config: {
					// Assumes '/data' is mounted via docker.volumes in plugin.yaml
					// and /data/sample.csv already exists on the host volume.
					sql: "SELECT * FROM read_csv_auto('/data/sample.csv') LIMIT 5"
				}
			}
		},
		{
			host: "_"
			plugin: {
				id:     "duckdb"
				action: "export_parquet"
				config: {
					// db_file is a path *inside the container*, not under /data —
					// this demo assumes /my_database.db already exists with a
					// users table (e.g. copy one into /var/honey/data and use
					// "/data/my_database.db" instead if starting from scratch).
					db_file: "my_database.db"
					query:   "SELECT * FROM users WHERE active = true"
					output:  "active_users.parquet"
				}
			}
		}
	]
}
