// System Hardening Verification
//
// Checks standard security best practices on Linux machines.
//
//   honey cue-exec examples/recipe/system_hardening_check.cue "<search-filter>"
//   honey cue-exec --execute examples/recipe/system_hardening_check.cue "<search-filter>"
recipe: {
	name: "system-hardening-check"
	type: "graph"

	steps: [
		{
			id:   "check-ssh-root"
			host: "*"
			command: "grep -i '^PermitRootLogin' /etc/ssh/sshd_config || echo 'PermitRootLogin not explicitly set'"
		},
		{
			id:   "check-firewall"
			host: "*"
			command: "ufw status || firewall-cmd --state || echo 'No standard firewall detected'"
		},
		{
			id:      "hardening-report"
			host:    "_"
			depends: ["check-ssh-root", "check-firewall"]
			ai: {
				prompt: """
Analyze the SSH root login configurations and firewall statuses across all hosts.
Flag any host where PermitRootLogin is 'yes' or where a firewall is inactive/missing.
"""
			}
		}
	]
}
