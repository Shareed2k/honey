// k6 docker-runtime plugin: run a k6 load test and get either the human text
// summary or a structured JSON summary back.
//
// honey overrides the image entrypoint with the honey-plugin-init shim, so
// argv[0] must be k6's absolute binary path (/usr/bin/k6, confirmed in
// grafana/k6:latest), not rely on the image's own ENTRYPOINT.
//
// The JS test script is passed on stdin (`k6 run -`), never bind-mounted — it
// maps to the action's `stdin:` field. Tunables (vus/duration/env) are passed
// as real argv flags, so nothing is shell-interpolated (no injection surface).

// Appended to the user script for run_json: k6 calls handleSummary() at the end
// of a test if it is defined, and uses its return value INSTEAD of printing the
// default text summary. Returning { stdout: ... } makes stdout carry exactly one
// JSON document, which output_format: "json" validates and a downstream step can
// consume via env_from.extract. No shell, no deprecated --summary-export.
// Do NOT also define handleSummary in your own script (duplicate export = error).
#summaryHook: """

	export function handleSummary(data) {
		return { stdout: JSON.stringify(data) };
	}
	"""

// version: smoke test — proves the image loads and the shim can exec k6.
actions: version: {
	argv: ["/usr/bin/k6", "version"]
	output_format: "text"
}

// run: human-readable run — k6's normal text summary on stdout (shown in
// results / usable as a notify body).
actions: run: {
	#Config: {
		// Required-with-default, not optional (`?`): evalAction decodes the
		// unified config back to a plain map to apply defaults, and CUE's Decode
		// omits uninstantiated optional fields — an optional field would vanish
		// before the second compile pass. `script` is required with no default,
		// so validation rejects a call that omits it.
		script:   string             // piped via stdin as `k6 run -`
		vus:      int    | *1
		duration: string | *"30s"
		quiet:    bool   | *true      // suppress the progress bar (stderr noise)
		env: {[string]: string} | *{} // exposed to the script as __ENV.*
	}
	argv: [
		"/usr/bin/k6", "run",
		"--vus", "\(config.vus)",
		"--duration", config.duration,
		if config.quiet {"--quiet"},
		for k, v in config.env for a in ["--env", "\(k)=\(v)"] {a},
		"-",
	]
	stdin: config.script
	output_format: "text"
}

// run_json: structured run — a single JSON end-of-test summary on stdout,
// shaped like k6's summary export (top-level `metrics`, `root_group`, etc.):
//
//   {"metrics": {
//       "http_reqs":          {"values": {"count": 120, "rate": 5.9}},
//       "http_req_failed":    {"values": {"rate": 0, "passes": 0, "fails": 120}},
//       "http_req_duration":  {"values": {"avg": 41.2, "p(95)": 88.4, ...}},
//       "checks":             {"values": {"rate": 1, "passes": 120, "fails": 0}}
//   }, ...}
//
// Extract fields downstream with env_from.extract, e.g.
//   p95: ".metrics.http_req_duration.values.\"p(95)\""
actions: run_json: {
	#Config: {
		script:   string
		vus:      int    | *1
		duration: string | *"30s"
		env: {[string]: string} | *{}
	}
	argv: [
		"/usr/bin/k6", "run", "--quiet",
		"--vus", "\(config.vus)",
		"--duration", config.duration,
		for k, v in config.env for a in ["--env", "\(k)=\(v)"] {a},
		"-",
	]
	stdin: config.script + #summaryHook
	output_format: "json"
}
