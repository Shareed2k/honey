// Demo recipe for the duckdb Docker plugin.
//
//   honey cue-exec --execute examples/recipe/duckdb_plugin_demo.cue <search-filter>
//
// Requires plugins.enabled: true and the duckdb plugin installed.
recipe: {
	name: "duckdb-plugin-demo"
	steps: [
		{
			host: "localhost"
			name: "query-data"
			plugin: {
				id:     "duckdb"
				action: "query"
				config: {
					// Assumes '/data' is mounted via docker.volumes in plugin.yaml
					sql: "SELECT * FROM read_csv_auto('/data/sample.csv') LIMIT 5"
				}
			}
			register: "query_result"
		},
		{
			host: "localhost"
			name: "export-parquet"
			plugin: {
				id:     "duckdb"
				action: "export_parquet"
				config: {
					db_file: "my_database.db"
					query:   "SELECT * FROM users WHERE active = true"
					output:  "active_users.parquet"
				}
			}
		}
	]
}
