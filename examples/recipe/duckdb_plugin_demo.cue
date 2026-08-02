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
					// Using generate_series to create dummy data inline so
					// the demo runs out of the box without needing external files.
					sql: "SELECT * FROM generate_series(1, 5) AS t(id)"
				}
			}
		},
		{
			host: "_"
			plugin: {
				id:     "duckdb"
				action: "export_parquet"
				config: {
					// This exports the dummy data to the temporary HONEY_WORKSPACE 
					// which is automatically mounted and cleaned up for this run.
					query:   "SELECT * FROM generate_series(1, 5) AS t(id)"
					output:  "${HONEY_WORKSPACE}/active_users.parquet"
				}
			}
		}
	]
}
