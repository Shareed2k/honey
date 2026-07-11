// Daily read-only vulnerability audit using the cve-scanner WASM plugin.
//
// Scans every target, captures the normalized CVE report, and emits an AI
// summary. No patching — safe to run anywhere (a "detect & prioritize" pass).
//
// Requires plugins.enabled: true and cve-scanner installed under
// ~/.config/honey/plugins/cve-scanner/.
//
//   honey cue-exec examples/recipe/daily_cve_audit.cue "<search-filter>"
//   honey cue-exec --execute examples/recipe/daily_cve_audit.cue "<search-filter>"
recipe: {
	name: "daily-cve-audit"
	type: "graph"
	schedules: { daily: {cron: "0 6 * * *", timezone: "UTC"} } // Run daily at 6 AM

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
					min_severity: "medium"
					only_fixed:   false // We want to see all vulnerabilities
				}
			}
		},
		{
			id:      "audit-report"
			host:    "_"
			depends: ["scan"]
			summarize: {
				prompt: """
Produce a vulnerability audit from the scan reports. 
List the top 10 CVEs by severity with affected hosts. 
Highlight any Critical vulnerabilities that currently have a fix available.
"""
			}
		},
	]
}
