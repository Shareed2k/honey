// Graph recipe: Prometheus self-health check via three parallel PromQL queries
// (target availability, scrape latency, series cardinality), then an AI summary.
//
// Uses only Prometheus's own self-scrape metrics (job="prometheus"), so it runs
// against a bare `docker run -p 9090:9090 prom/prometheus` with no extra exporters.
//
// Simpler single-query starting point: prometheus_query_demo.cue
//
// Install:
//   make build-plugin-modules
//   mkdir -p ~/.config/honey/plugins/prometheus
//   cp examples/plugins/prometheus/plugin.yaml examples/plugins/prometheus/plugin.wasm ~/.config/honey/plugins/prometheus/
//   # edit ~/.config/honey/plugins/prometheus/plugin.yaml: config.prometheus_url + allowed_hosts
//
// Validate:
//   honey cue-validate examples/recipe/prometheus_health_check.cue
// Plan (no AI call, no OPENAI_API_KEY needed):
//   honey cue-exec examples/recipe/prometheus_health_check.cue
// Run (calls the configured LLM for the summary step):
//   honey cue-exec --execute examples/recipe/prometheus_health_check.cue
//
// Optional: add a `notify:` block to the "summarize" step (see
// postgres_replica_lag_plugin.cue for the pattern) to push the AI summary to
// Slack/HTTP/etc. instead of just capturing it as step output.
recipe: {
	name: "prometheus-health-check"
	type: "graph"

	steps: [
		{
			id:   "targets_up"
			host: "_"
			plugin: {
				id:     "prometheus"
				action: "query"
				config: {
					query:   "up"
					timeout: "10s"
				}
			}
		},
		{
			id:   "scrape_duration"
			host: "_"
			plugin: {
				id:     "prometheus"
				action: "query"
				config: {
					query:   "max(scrape_duration_seconds)"
					timeout: "10s"
				}
			}
		},
		{
			id:   "series_count"
			host: "_"
			plugin: {
				id:     "prometheus"
				action: "query"
				config: {
					query:   "prometheus_tsdb_head_series"
					timeout: "10s"
				}
			}
		},
		{
			id:   "summarize"
			host: "_"
			depends: [
				"targets_up",
				"scrape_duration",
				"series_count",
			]
			ai: {
				prompt: """
Summarize Prometheus's own health from the three query results above: are all
scrape targets up, is scrape latency reasonable (under ~1s), and is the active
series count in a sane range. Flag anything that looks wrong in 3-5 bullets.
"""
				model: "gpt-4o-mini"
			}
			output: "health_summary"
		},
	]
}
