// Package cmdgate is the single decision point for whether a command may run on
// a host. It combines honey's deterministic command-risk analysis (which the LLM
// cannot override) with an optional OPA policy enforcer, so every caller — the
// recipe engine, the web API, and the MCP server — gates exec the same way.
package cmdgate

import (
	"context"
	"strings"

	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/policy"
)

// Decide reports whether a single command/target is denied.
//
// Built-in critical risk signals (mkfs, dd to a block device, recursive chmod of
// a system path, curl|sh, …) always deny, even when enforcer is nil — the
// secure-by-default floor. For non-critical commands, a nil enforcer allows;
// otherwise the enforcer evaluates input and a verdict of deny / require_approval
// / require_biometric denies in non-interactive callers with a clear reason.
//
// analysis is the result of commandrisk.Analyze/AnalyzeStep for the command.
// input is the policy input map the caller builds (action, actor, command,
// target, …); it is passed to enforcer.Evaluate verbatim.
func Decide(ctx context.Context, enforcer *policy.Enforcer, analysis commandrisk.Analysis, input map[string]any) (reason string, denied bool, err error) {
	if crit := analysis.FirstCritical(); crit != nil {
		return "command risk: " + crit.Reason, true, nil
	}
	if enforcer == nil {
		return "", false, nil
	}

	d, err := enforcer.Evaluate(ctx, input)
	if err != nil {
		return "", false, err
	}
	if d.Allow && d.Decision != "require_approval" && d.Decision != "require_biometric" && d.Decision != "deny" {
		return "", false, nil
	}

	reason = d.DenyReason
	if reason == "" {
		reason = "denied by policy"
	}
	if len(d.Requires) > 0 {
		reason += " (requires: " + strings.Join(d.Requires, ", ") + ")"
	}
	return reason, true, nil
}
