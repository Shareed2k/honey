package honey

import rego.v1

# Example OPA guardrail for honey command execution.
#
# honey has no built-in command guardrails — OPA is the only command gate, so
# whatever this policy allows, runs. It denies by exception (default allow),
# which suits a starting point; flip `default allow := false` for an allowlist
# posture instead (see ../mcp/mcp_exec.rego).
#
# Point one honey instance at ONE of these example directories:
#   honey exec --policy-dir examples/policy/command-guardrail ...
# The loader reads a single directory, non-recursively, and every example here
# declares `package honey`, so loading two of them at once is a conflict.
#
# `input.command` is NOT one shape. Read the shapes you mean to gate, or a rule
# silently never fires:
#   * `honey exec`, MCP    -> object: raw, detected, max_severity, interpreter
#                             (internal/cli/exec_check.go,
#                              internal/mcpserver/exec.go)
#   * interactive terminal, SSH gateway
#                          -> plain string (cmdgate.CommandPolicyInput)
#   * recipe command/script steps
#                          -> object: raw + interpreter only, no risk fields
#                             (internal/engine/risk_assess.go)

default allow := true

default deny_reason := ""

allow := false if count(reasons) > 0

# ONE deny_reason rule over a set of reasons, never several `deny_reason :=`
# rules: two of those can hold for the same command (a critical `rm` on a prod
# host trips both rules below), and OPA then fails evaluation outright with
# "complete rules must not produce multiple outputs" — an error surfaced to the
# caller instead of a clean deny with a message.
deny_reason := concat("; ", sort(reasons)) if count(reasons) > 0

# The command text, whichever shape it arrived in.
cmd_raw := input.command if is_string(input.command)

cmd_raw := input.command.raw if is_object(input.command)

gated_actions := ["command_exec", "mcp_exec"]

destructive_commands := ["rm", "dd", "mkfs", "fdisk", "parted"]

destructive_msg := "destructive filesystem operations are forbidden on production targets"

# 1. Refuse whatever commandrisk classified as critical or high severity. Only
#    the exec and MCP paths carry this field.
reasons contains sprintf("command risk severity is %v", [input.command.max_severity]) if {
	input.action in gated_actions
	is_object(input.command)
	input.command.max_severity in ["critical", "high"]
}

# 2a. Destructive command on a production target, read from the parsed command
#     surface where honey provides one — quoting- and path-aware, so it is the
#     primary check.
reasons contains destructive_msg if {
	input.action in gated_actions
	input.target.env == "prod"
	some cmd in input.command.detected.commands
	cmd in destructive_commands
}

# 2b. The same rule for the call sites that carry only the raw command text.
#     The reason string is identical to 2a's on purpose: when both fire the set
#     holds one element rather than two conflicting outputs. Text matching is
#     the weaker check — a fallback, not the primary one.
reasons contains destructive_msg if {
	input.action in gated_actions
	input.target.env == "prod"
	regex.match(`(^|[;|&]\s*)(rm|dd|mkfs(\.[a-z0-9]+)?|fdisk|parted)\b`, cmd_raw)
}
