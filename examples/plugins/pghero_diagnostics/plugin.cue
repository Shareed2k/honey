import "strings"

// PgHero read-only Postgres diagnostics. The container runs diagnose.rb, which
// loads the pghero gem, connects with DATABASE_URL, runs an allow-listed set of
// checks and prints one JSON object to stdout.
//
// DATABASE_URL is injected as a child-process env var (action `env:`), NOT into
// argv, so the connection string never appears in `ps`/proc inside the
// container. Pass it from the recipe as a secret expanded into config.database_url.
actions: diagnose: {
	#Config: {
		database_url: string
		// required-with-default (NOT optional) so the CUE default survives the
		// plugin loader's decode/recompile pass — see examples/plugins/watchtower.
		checks: [...string] | *["connections", "index_usage", "space", "running_queries"]
	}
	argv: [
		"ruby", "/app/diagnose.rb",
		"--checks", strings.Join(config.checks, ","),
	]
	env: {
		DATABASE_URL: config.database_url
	}
	output_format: "json"
}

// Convenience action: only the slow_queries check. Needs pg_stat_statements on
// the target DB (else diagnose.rb returns an error entry for the check, without
// failing the whole run).
actions: slow_queries: {
	#Config: {
		database_url: string
	}
	argv: ["ruby", "/app/diagnose.rb", "--checks", "slow_queries"]
	env: {
		DATABASE_URL: config.database_url
	}
	output_format: "json"
}
