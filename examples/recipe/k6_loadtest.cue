// Load-test a URL with the k6 docker-runtime plugin, then report p95 latency
// and failure rate.
//
//   honey cue-exec --execute examples/recipe/k6_loadtest.cue
//
// Requires: plugins.enabled: true and the k6 plugin installed
// (examples/plugins/k6).
//
// host targeting:
//   host: "_"                 -> runs on the operator's local Docker daemon (this recipe)
//   'prod-*' / any real host  -> runs k6 on THAT host's daemon over SSH (honey
//                                stages the honey-plugin-init shim to /tmp on the
//                                host; nothing else is installed). Set the step
//                                host to the glob and run:
//                                  honey cue-exec --execute examples/recipe/k6_loadtest.cue 'prod-*'
// The target URL must be reachable from wherever the container runs.
//
// loadtest uses "run_json" so report can extract structured fields; swap to the
// plain-text "run" action if you only want the human-readable summary in results
// (and drop the report step). See examples/plugins/k6/plugin.cue for the JSON shape.
//
// NOTE: the script sets NO k6 thresholds on purpose. A threshold breach makes k6
// exit non-zero, and honey only propagates a step's stdout to a downstream
// env_from when the step succeeded (run.go) — so a failing threshold would leave
// report with nothing to extract. The report step therefore judges pass/fail
// itself from the extracted failure rate. env_from is capped at 8192 bytes per
// value; for very large summaries use plugin.kv_key (64KB) instead.
recipe: {
	name: "k6-loadtest"
	type: "graph"
	steps: [
		{
			id:   "loadtest"
			host: "_"
			plugin: {
				id:     "k6"
				action: "run_json"
				config: {
					vus:      5
					duration: "20s"
					env: {TARGET_URL: "https://test.k6.io"}
					script: """
						import http from 'k6/http';
						import { check, sleep } from 'k6';
						export default function () {
							const res = http.get(__ENV.TARGET_URL);
							check(res, { 'status is 200': (r) => r.status === 200 });
							sleep(1);
						}
						"""
				}
			}
		},
		{
			id:      "report"
			host:    "_"
			depends: ["loadtest"]
			env_from: [{
				step: "loadtest"
				extract: {
					reqs:      ".metrics.http_reqs.values.count"
					p95_ms:    ".metrics.http_req_duration.values.\"p(95)\""
					fail_rate: ".metrics.http_req_failed.values.rate"
				}
			}]
			command: """
				echo "k6 on $(hostname): $reqs reqs, p95=${p95_ms}ms, fail_rate=$fail_rate"
				# derive pass/fail here (a k6 threshold breach would stop the recipe
				# before this step ever runs — see the header note)
				if awk "BEGIN{ exit !($fail_rate <= 0.01) }"; then
					echo "PASS: failure rate within budget"
				else
					echo "FAIL: failure rate too high"
				fi
				"""
			notify: {
				notify_subject: "k6 load test report"
			}
		},
	]
}
