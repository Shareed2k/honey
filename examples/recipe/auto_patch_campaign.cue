// Auto-Patch campaign with a compliance gate.
//
// Flow: scan -> gate -> patch -> report.
//   1. scan  : run scanner on every target.
//   2. gate  : block the campaign when criticals exceed the allowed budget (5).
//   3. patch : apply security-only package upgrades.
//   4. report: AI summary of what was found and fixed.
//
// Requires plugins.enabled: true and cve-scanner installed under
// ~/.config/honey/plugins/cve-scanner/.
//
//   honey cue-exec examples/recipe/auto_patch_campaign.cue "<search-filter>"
//   honey cue-exec --execute examples/recipe/auto_patch_campaign.cue "<search-filter>"
recipe: {
	name: "auto-patch-campaign"
	type: "graph"
	schedules: { weekly: {cron: "0 2 * * 0", timezone: "UTC"} } // Weekly on Sunday 2 AM

	defaults: {
		// Cap blast radius: patch a few hosts at a time.
		max_parallel: 5
	}

	steps: [
		{
			id:   "scan"
			host: "*"
			plugin: {
				id:     "cve-scanner"
				action: "scan"
				config: {
					scanner:      "auto"
					target:       "dir:/"
					min_severity: "high"
					only_fixed:   true // Only care about fixable issues
				}
			}
		},
		{
			id:      "gate"
			host:    "*"
			depends: ["scan"]
			env_from: [{
				step: "scan"
				extract: CRITICAL: ".by_severity.critical // 0"
			}]
			// Fail the host if it has more than 5 critical CVEs requiring manual review.
			// Patching still runs for hosts that pass; failed hosts surface in the report.
			command:       "test \"${CRITICAL:-0}\" -le 5 || { echo \"critical budget exceeded: ${CRITICAL}\" >&2; exit 1; }"
			ignore_errors: true // surface in the report, don't abort the entire campaign
		},
		{
			id:      "patch"
			host:    "*"
			depends: ["gate"]
			plugin: {
				id:     "cve-scanner"
				action: "patch"
				config: {
					manager:       "auto"
					security_only: true
				}
			}
			retry: {attempts: 2, delay_ms: 5000, backoff: "exponential"}
		},
		{
			id:      "report"
			host:    "_"
			depends: ["patch"]
			ai: {
				prompt: """
Summarize this patch campaign in 4-6 bullets: 
Which hosts exceeded the critical budget?
Which patches were successfully applied?
End with a one-line risk verdict.
"""
			}
		}
	]
}
