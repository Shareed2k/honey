// Example recipe using the prometheus WASM plugin (PromQL instant query).
//
// The Prometheus server URL lives in plugins/prometheus/plugin.yaml (config.prometheus_url),
// not in the recipe — only the query itself is per-step. Optional auth token via
// PROMETHEUS_BEARER_TOKEN env var (declared in allowed_env), no auth is fine for local dev.
//
// Install:
//   make build-plugin-modules
//   mkdir -p ~/.config/honey/plugins/prometheus
//   cp examples/plugins/prometheus/plugin.yaml examples/plugins/prometheus/plugin.wasm ~/.config/honey/plugins/prometheus/
//   # then edit ~/.config/honey/plugins/prometheus/plugin.yaml: config.prometheus_url + allowed_hosts
//
//   honey cue-exec examples/recipe/prometheus_query_demo.cue
//   honey cue-exec --execute examples/recipe/prometheus_query_demo.cue
//
// The step's output is JSON: {"type":"vector","result":[...],"scalar":<n>,"warnings":[...]}.
// "scalar" is set when the query resolves to exactly one series or a scalar value —
// e.g. the "up" query below returns it when only one target is scraped.
recipe: {
	name: "prometheus-query-demo"

	steps: [{
		host: "_"
		plugin: {
			id:     "prometheus"
			action: "query"
			config: {
				query:   "up"
				timeout: "10s"
			}
		}
		output: "prom_up"
	}]
}
