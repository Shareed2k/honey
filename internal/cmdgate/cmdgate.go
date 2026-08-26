// Package cmdgate is the single decision point for whether a command may run on
// a host. It combines honey's deterministic command-risk analysis (fed to OPA as
// data, never a gate by itself) with an optional OPA policy enforcer, so every
// caller — the recipe engine, the web API, and the MCP server — gates exec the
// same way. OPA is the only command-authorization gate: a nil enforcer allows
// unconditionally.
package cmdgate

import (
	"context"
	"strings"

	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
)

// Result is the outcome of gating a single command/target: whether it is denied
// and the human-readable reason when it is.
type Result struct {
	Denied bool
	Reason string
}

// TargetInput carries the name and pre-built policy input map for one target.
// Callers build PolicyInput from their own host types; cmdgate stays ignorant
// of hosts.Record so the import graph stays clean.
type TargetInput struct {
	Name        string
	PolicyInput map[string]any
}

// TargetDecision is the policy verdict for one target.
type TargetDecision struct {
	Name   string
	Reason string
	Denied bool
}

// AssessTargets runs commandrisk.AnalyzeStep once for the given command, then
// calls Decide for each TargetInput. The shared analysis is returned so callers
// can surface it (and fold its signals into their own policy input, e.g.
// input.command.max_severity) without repeating the parse.
//
// When summaryOnly is true only the first target is evaluated — this matches
// dry-run / preview semantics where a representative verdict is enough.
// When summaryOnly is false every target is evaluated — this matches the runtime
// gate where per-host decisions are needed.
func AssessTargets(ctx context.Context, enforcer *policy.Enforcer, rawCommand, interpreter string, targets []TargetInput, summaryOnly bool) (analysis commandrisk.Analysis, decisions []TargetDecision, err error) {
	analysis = commandrisk.AnalyzeStep(rawCommand, interpreter)
	eval := targets
	if summaryOnly && len(eval) > 1 {
		eval = eval[:1]
	}
	for _, t := range eval {
		res, decErr := Decide(ctx, enforcer, t.PolicyInput)
		if decErr != nil {
			return analysis, nil, decErr
		}
		decisions = append(decisions, TargetDecision{Name: t.Name, Reason: res.Reason, Denied: res.Denied})
	}
	return analysis, decisions, nil
}

// Decide reports whether a single command/target is denied:
//
//  1. Nil enforcer: allow. OPA is the only command-authorization gate honey
//     has — with none configured, nothing is denied.
//  2. OPA policy: the enforcer evaluates input; a verdict of deny /
//     require_approval / require_biometric denies in non-interactive callers
//     with a clear reason.
//
// input is the policy input map the caller builds (action, actor, command,
// target, …); it is passed to enforcer.Evaluate verbatim.
func Decide(ctx context.Context, enforcer *policy.Enforcer, input map[string]any) (Result, error) {
	if enforcer == nil {
		return Result{}, nil
	}

	d, err := enforcer.Evaluate(ctx, input)
	if err != nil {
		return Result{}, err
	}
	if d.Allow && d.Decision != "require_approval" && d.Decision != "require_biometric" && d.Decision != "deny" {
		return Result{}, nil
	}

	reason := d.DenyReason
	if reason == "" {
		reason = "denied by policy"
	}
	if len(d.Requires) > 0 {
		reason += " (requires: " + strings.Join(d.Requires, ", ") + ")"
	}
	return Result{Denied: true, Reason: reason}, nil
}

// CommandPolicyInput builds the OPA input for a command_exec decision, shared
// by the SSH gateway's per-command interactive guard and the web/share
// terminal guard so one rego policy shape serves both. Do not change these
// keys: existing policies depend on
// input.action/actor/command/target.{name,provider,env,groups}.
func CommandPolicyInput(actor string, rec hosts.Record, command string) map[string]any {
	return map[string]any{
		"action":  "command_exec",
		"actor":   actor,
		"command": command,
		"target":  TargetPolicyInput(rec),
	}
}

// TargetPolicyInput builds the OPA target sub-input shared by CommandPolicyInput
// and any other action needing the same target shape (e.g. the SSH gateway's
// and web server's interactive_session gate).
func TargetPolicyInput(rec hosts.Record) map[string]any {
	return map[string]any{
		"name":     rec.Name,
		"provider": rec.Provider,
		"env":      rec.Meta["env"],
		"groups":   rec.Groups,
	}
}
