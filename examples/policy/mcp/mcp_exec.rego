package honey

import rego.v1

# Example OPA policy for commands run over MCP (Model Context Protocol).
#
# Allowlist posture: deny by default, permit a short list of read-only
# commands. The MCP path always sends `command` as an object carrying raw,
# detected, max_severity and interpreter — see internal/mcpserver/exec.go.
#
# Load one example directory at a time:
#   honey mcp --policy-dir examples/policy/mcp

default allow := false

default deny_reason := ""

readonly_commands := ["ls", "cat", "ps", "top", "free", "df", "uname", "uptime"]

allow if {
	input.action == "mcp_exec"
	input.actor == "mcp"
	count(reasons) == 0
	some cmd in readonly_commands
	startswith(input.command.raw, cmd)
}

# One deny_reason rule over a set of reasons, as in ../command-guardrail: two
# separate `deny_reason :=` rules that can both hold for the same command (a
# critical `rm`) make OPA fail evaluation with "complete rules must not produce
# multiple outputs" instead of denying with a message.
deny_reason := concat("; ", sort(reasons)) if count(reasons) > 0

reasons contains "critical commands are disabled over MCP" if {
	input.command.max_severity == "critical"
}

reasons contains "rm is forbidden over MCP" if {
	startswith(input.command.raw, "rm")
}
