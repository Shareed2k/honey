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
					// We query the CSV directly using read_csv_auto instead of
					// relying on a pre-existing table in a pre-existing database.
					// This uses the default ":memory:" database.
					query:   "SELECT * FROM read_csv_auto('/data/sample.csv') LIMIT 5"
					output:  "active_users.parquet"
				}
			}
		}
	]
}
