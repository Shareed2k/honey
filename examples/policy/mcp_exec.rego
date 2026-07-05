package honey

import rego.v1

# This policy evaluates commands run via MCP (Model Context Protocol).
default allow := false

# By default, deny any destructive command using commandrisk severity
deny_reason := "critical commands are disabled over MCP" if {
	input.command.max_severity == "critical"
}

# Allow simple non-destructive commands
allow if {
	input.action == "mcp_exec"
	input.actor == "mcp"
	input.command.max_severity != "critical"
	
	allowed_commands := ["ls", "cat", "ps", "top", "free", "df", "uname", "uptime"]
	some cmd in allowed_commands
	startswith(input.command.raw, cmd)
}

# Always deny rm commands
deny_reason := "rm is forbidden over MCP" if {
	startswith(input.command.raw, "rm")
}
