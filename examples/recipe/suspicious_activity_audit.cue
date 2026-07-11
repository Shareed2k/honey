// Suspicious Process & Open Port Audit
//
// Looks for anomalies in running processes and listening ports, which could indicate a compromise.
//
//   honey cue-exec examples/recipe/suspicious_activity_audit.cue "<search-filter>"
//   honey cue-exec --execute examples/recipe/suspicious_activity_audit.cue "<search-filter>"
recipe: {
	name: "suspicious-activity-audit"
	type: "graph"
	
	steps: [
		{
			id:   "list-listening-ports"
			host: "*"
			command: "ss -tulnp"
		},
		{
			id:   "high-cpu-processes"
			host: "*"
			command: "ps -eo pid,ppid,cmd,%cpu,%mem --sort=-%cpu | head -n 10"
		},
		{
			id:      "anomaly-detection"
			host:    "_"
			depends: ["list-listening-ports", "high-cpu-processes"]
			summarize: {
				prompt: """
Act as a security analyst. Review the listening ports and top processes across these hosts.
Identify any non-standard listening ports (e.g., something other than 22, 80, 443, etc.) 
or processes that look like cryptominers or suspicious scripts.
"""
			}
		}
	]
}
