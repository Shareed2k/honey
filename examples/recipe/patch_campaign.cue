// Patch & vulnerability-management campaign using the cve-scanner WASM plugin.
//
// Flow (Rudder-style): scan -> gate -> patch -> report.
//   1. scan  : run grype/trivy on every target, emit a normalized CVE report.
//   2. gate  : block the campaign when criticals exceed the allowed budget.
//   3. patch : apply security-only package upgrades.
//   4. report: AI summary of what was found and fixed.
//
// Requires plugins.enabled: true and cve-scanner installed under
// ~/.config/honey/plugins/cve-scanner/ (plugin.yaml + plugin.wasm).
//
//   honey cue-exec examples/recipe/patch_campaign.cue "<search-filter>"
//   honey cue-exec --execute examples/recipe/patch_campaign.cue "<search-filter>"
recipe: {
	name: "patch-campaign"
	type: "graph"

	defaults: {
		// Cap blast radius: patch a few hosts at a time (canary -> rollout).
		max_parallel: 5
	}

	// Optional: run automatically on a schedule (a patch campaign).
	// schedules: [{cron: "0 3 * * 6", timezone: "UTC"}]

	steps: [
		// 1. Scan every target. stdout is a JSON report:
		//    {scanner, target, total, by_severity:{...}, cves:[{id,severity,package,installed,fixed}]}
		{
			id:   "scan"
			host: "*"
			plugin: {
				id:     "cve-scanner"
				action: "scan"
				config: {
					scanner:      "auto" // auto | grype | trivy
					target:       "dir:/"
					min_severity: "high" // negligible|low|medium|high|critical
					only_fixed:   true   // ignore CVEs with no fix available
				}
			}
		},

		// 2. Compliance gate: pull the critical count out of the scan report and
		//    fail the host when it exceeds the budget (0 here). Patching still
		//    runs for hosts that pass; failed hosts surface in the report.
		{
			id:      "gate"
			host:    "*"
			depends: ["scan"]
			env_from: [{
				step: "scan"
				extract: CRITICAL: ".by_severity.critical // 0"
			}]
			// Nonzero exit marks the host failed when the budget is exceeded.
			command:       "test \"${CRITICAL:-0}\" -eq 0 || { echo \"critical budget exceeded: ${CRITICAL}\" >&2; exit 1; }"
			ignore_errors: true // surface in the report, don't abort the campaign
		},

		// 3. Apply security-only upgrades. Detects apt/dnf/yum/apk/zypper.
		//    Dry-run (no --execute) lists pending upgrades instead of applying.
		{
			id:      "patch"
			host:    "*"
			depends: ["scan"]
			plugin: {
				id:     "cve-scanner"
				action: "patch"
				config: {
					manager:       "auto"
					security_only: true
					// packages: ["openssl", "libc6"]  // pin to specific packages
				}
			}
			retry: {attempts: 2, delay_ms: 5000, backoff: "exponential"}
		},

		// 4. AI report (must be last; runs locally).
		{
			id:      "report"
			host:    "_"
			depends: ["patch"]
			summarize: {
				prompt: """
Summarize this patch campaign in 4-6 bullets: how many CVEs per host by
severity, which hosts exceeded the critical budget, and which patch steps
failed. End with a one-line risk verdict.
"""
			}
		},
	]
}
