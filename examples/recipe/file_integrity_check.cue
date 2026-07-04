// File Integrity & Configuration Drift Check
//
// Hashes critical files and compares them across hosts to detect drift.
//
//   honey cue-exec examples/recipe/file_integrity_check.cue "<search-filter>"
//   honey cue-exec --execute examples/recipe/file_integrity_check.cue "<search-filter>"
recipe: {
	name: "file-integrity-check"
	type: "graph"
	schedules: { daily: {cron: "0 8 * * *", timezone: "UTC"} } // Daily at 8 AM

	steps: [
		{
			id:   "hash-critical-files"
			host: "*"
			command: """
			sha256sum /etc/passwd /etc/shadow /etc/ssh/sshd_config 2>/dev/null || true
			"""
		},
		{
			id:      "report-drift"
			host:    "_"
			depends: ["hash-critical-files"]
			ai: {
				prompt: """
Review the file hashes from the hosts. Group the hosts by the hash of their /etc/ssh/sshd_config.
Alert if any hosts have a differing /etc/shadow or /etc/passwd structure than the majority.
"""
			}
		}
	]
}
