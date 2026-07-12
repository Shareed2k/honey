actions: query: {
	#Config: {
		db_file: string | *":memory:"
		sql:      string
	}
	
	argv: [
		"duckdb",
		"-json",
		config.db_file,
		"-c", config.sql
	]
	
	output_format: "json"
}

actions: export_parquet: {
	#Config: {
		db_file: string | *":memory:"
		query:   string
		output:  string
	}
	
	argv: [
		"duckdb",
		config.db_file,
		"-c", "COPY (\(config.query)) TO '/data/\(config.output)' (FORMAT PARQUET);"
	]
	
	output_format: "text"
}
