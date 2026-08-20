// Package cmdgate is the single decision point for whether a command may run on
// a host. It combines honey's deterministic command-risk analysis (which the LLM
// cannot override) with an optional OPA policy enforcer, so every caller — the
// recipe engine, the web API, and the MCP server — gates exec the same way.
package cmdgate

import (
	"context"
	"strings"

	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/guardrails"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
)

// Result is the outcome of gating a single command/target: whether it is denied,
// the human-readable reason when it is, and any non-fatal guardrail warnings
// collected along the way (warn-action rules that matched but did not block).
type Result struct {
	Denied   bool
	Reason   string
	Warnings []string
}

// TargetInput carries the name, pre-built policy input map, and guardrail
// attributes for one target. Callers build PolicyInput and Attrs from their own
// host types; cmdgate stays ignorant of hosts.Record so the import graph stays
// clean.
type TargetInput struct {
	Name        string
	PolicyInput map[string]any
	Attrs       guardrails.Attrs
}

// TargetDecision is the risk+policy+guardrail verdict for one target.
type TargetDecision struct {
	Name     string
	Reason   string
	Denied   bool
	Warnings []string
}

// AssessTargets runs commandrisk.AnalyzeStep once for the given command, then
// calls Decide for each TargetInput (passing rawCommand as the guardrail text
// and the target's Attrs for guardrail Targets scoping). The shared analysis is
// returned so callers can surface it in dry-run UIs without repeating the parse.
//
// rules is the optional guardrail floor: a nil (or empty) ruleset makes the
// guardrail layer a no-op, reproducing the pre-guardrail behavior exactly.
//
// When summaryOnly is true only the first target is evaluated — this matches
// dry-run / preview semantics where a representative verdict is enough.
// When summaryOnly is false every target is evaluated — this matches the runtime
// gate where per-host decisions are needed.
func AssessTargets(ctx context.Context, enforcer *policy.Enforcer, rules *guardrails.Ruleset, rawCommand, interpreter string, targets []TargetInput, summaryOnly bool) (analysis commandrisk.Analysis, decisions []TargetDecision, err error) {
	analysis = commandrisk.AnalyzeStep(rawCommand, interpreter)
	eval := targets
	if summaryOnly && len(eval) > 1 {
		eval = eval[:1]
	}
	for _, t := range eval {
		res, decErr := Decide(ctx, enforcer, rules, analysis, t.PolicyInput, rawCommand, t.Attrs)
		if decErr != nil {
			return analysis, nil, decErr
		}
		decisions = append(decisions, TargetDecision{Name: t.Name, Reason: res.Reason, Denied: res.Denied, Warnings: res.Warnings})
	}
	return analysis, decisions, nil
}

// Decide reports whether a single command/target is denied, in a fixed order of
// deterministic floors before the (optional) OPA policy:
//
//  1. Critical risk floor: built-in critical risk signals (mkfs, dd to a block
//     device, recursive chmod of a system path, curl|sh, …) always deny, even
//     when enforcer and rules are nil — the secure-by-default floor, first.
//  2. Guardrail floor: when rules is non-nil and non-empty, the operator-defined
//     ruleset is evaluated against rawText. A matched deny rule blocks; matched
//     warn rules are collected into Result.Warnings and carried through the rest
//     of the decision. A nil or empty ruleset skips this step entirely, so
//     behavior is byte-for-byte identical to the pre-guardrail gate.
//  3. Nil enforcer: allow (carrying any guardrail warnings).
//  4. OPA policy: the enforcer evaluates input; a verdict of deny /
//     require_approval / require_biometric denies in non-interactive callers
//     with a clear reason. An allow carries the guardrail warnings through.
//
// analysis is the result of commandrisk.Analyze/AnalyzeStep for the command.
// input is the policy input map the caller builds (action, actor, command,
// target, …); it is passed to enforcer.Evaluate verbatim. rawText is the raw
// command being assessed — passed explicitly rather than read from input because
// input["command"] is not consistently a string across callers (a bare string
// in the SSH gateway, a nested map in the engine and MCP paths). attrs scopes
// guardrail Targets globs to the resource being acted on.
func Decide(ctx context.Context, enforcer *policy.Enforcer, rules *guardrails.Ruleset, analysis commandrisk.Analysis, input map[string]any, rawText string, attrs guardrails.Attrs) (Result, error) {
	if crit := analysis.FirstCritical(); crit != nil {
		return Result{Denied: true, Reason: "command risk: " + crit.Reason}, nil
	}

	var warnings []string
	if rules != nil && !rules.Empty() {
		v := rules.Evaluate(rawText, guardrails.KindCommand, attrs)
		if v.Denied {
			return Result{Denied: true, Reason: v.Reason}, nil
		}
		warnings = v.Warnings
	}

	if enforcer == nil {
		return Result{Warnings: warnings}, nil
	}

	d, err := enforcer.Evaluate(ctx, input)
	if err != nil {
		return Result{}, err
	}
	if d.Allow && d.Decision != "require_approval" && d.Decision != "require_biometric" && d.Decision != "deny" {
		return Result{Warnings: warnings}, nil
	}

	reason := d.DenyReason
	if reason == "" {
		reason = "denied by policy"
	}
	if len(d.Requires) > 0 {
		reason += " (requires: " + strings.Join(d.Requires, ", ") + ")"
	}
	return Result{Denied: true, Reason: reason, Warnings: warnings}, nil
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
		"target":  targetInput(rec),
	}
}

func targetInput(rec hosts.Record) map[string]any {
	return map[string]any{
		"name":     rec.Name,
		"provider": rec.Provider,
		"env":      rec.Meta["env"],
		"groups":   rec.Groups,
	}
}

// RecordAttrs builds the guardrail Targets-scoping attributes from a record.
func RecordAttrs(rec hosts.Record) guardrails.Attrs {
	return guardrails.Attrs{Provider: rec.Provider, Groups: rec.Groups, Name: rec.Name}
}
