// Read-only vulnerability audit using the cve-scanner WASM plugin.
//
// Scans every target, captures the normalized CVE report, and emits an AI
// summary. No patching — safe to run anywhere (a "detect & prioritize" pass).
//
// Requires plugins.enabled: true and cve-scanner installed under
// ~/.config/honey/plugins/cve-scanner/.
//
//   honey cue-exec examples/recipe/cve_scan_audit.cue "<search-filter>"
//   honey cue-exec --execute examples/recipe/cve_scan_audit.cue "<search-filter>"
recipe: {
	name: "cve-scan-audit"
	type: "graph"
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
					only_fixed:   false // audit shows everything, fixed or not
				}
			}
		},
		{
			id:      "audit"
			host:    "_"
			depends: ["scan"]
			summarize: {
				prompt: """
Produce a vulnerability audit from the scan reports: top 10 CVEs by severity
with affected hosts and packages, the count of systems impacted per critical
CVE, and which findings have a fix available. End with a prioritized
remediation list.
"""
			}
		},
	]
}
