package engine

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/guardrails"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
)

// riskDisableEnv, when set to a truthy value, bypasses the command risk gate
// entirely (including built-in critical denies). Escape hatch for trusted runs.
const riskDisableEnv = "HONEY_RISK_DISABLE"

// actorOrAPI returns the actor id, defaulting to "api" when empty.
func actorOrAPI(actor string) string {
	if strings.TrimSpace(actor) == "" {
		return "api"
	}
	return actor
}

// riskGateDisabled reports whether the gate is turned off via the environment.
func riskGateDisabled() bool {
	v := strings.TrimSpace(os.Getenv(riskDisableEnv))
	return v != "" && v != "0" && !strings.EqualFold(v, "false")
}

// gateCommandRisk analyzes a raw shell command and decides, per target, whether
// it may run. Built-in critical patterns always deny (unless HONEY_RISK_DISABLE).
// When an OPA enforcer is configured it makes the contextual decision via the
// "command_exec" action; require_approval / require_biometric verdicts deny with
// a clear reason in this non-interactive path (the approval flow is a later phase).
// Returns the allowed targets and skip results for denied ones — mirroring
// filterTargetsByPolicy so denied hosts stay visible in the run output.
func gateCommandRisk(ctx context.Context, opts ExecutionOptions, kind, rawCommand, interpreter string, targets []TargetContext) (allowed []TargetContext, skipped []HostExecResult, err error) {
	if riskGateDisabled() || strings.TrimSpace(rawCommand) == "" {
		return targets, nil, nil
	}

	analysis := commandrisk.AnalyzeStep(rawCommand, interpreter)
	enforcer := opts.Enforcer
	actor := actorOrAPI(opts.ActorID)

	for _, t := range targets {
		reason, denied, warnings, evalErr := commandRiskDecision(ctx, enforcer, opts.Guardrails, actor, kind, rawCommand, analysis, opts, t.Record)
		if evalErr != nil {
			return nil, nil, evalErr
		}
		// Guardrail warn-action rules are non-fatal: log the rule message (no
		// captured command text) on the step + host so operators have a trail,
		// then let the command through.
		for _, w := range warnings {
			zap.L().Warn("guardrail warning",
				zap.String("step", kind), zap.String("host", t.Record.Name), zap.String("message", w))
		}
		if !denied {
			allowed = append(allowed, t)
			continue
		}
		sk := WhenSkippedResult(t.Record)
		sk.Output = "(blocked: " + reason + ")"
		skipped = append(skipped, sk)
	}
	return allowed, skipped, nil
}

// RiskStepFilter wraps gateCommandRisk as a StepFilter so the risk gate
// participates in the same pipeline interface as policyStepFilter and whenStepFilter.
type RiskStepFilter struct {
	opts        ExecutionOptions
	kind        string
	rawCommand  string
	interpreter string
}

// NewRiskStepFilter returns a StepFilter that gates targets via the command
// risk analysis (built-in critical signals + OPA command_exec decision).
func NewRiskStepFilter(opts ExecutionOptions, kind, rawCommand, interpreter string) *RiskStepFilter {
	return &RiskStepFilter{opts: opts, kind: kind, rawCommand: rawCommand, interpreter: interpreter}
}

// Filter applies the command risk gate to the given targets.
func (f *RiskStepFilter) Filter(ctx context.Context, targets []TargetContext) ([]TargetContext, []HostExecResult, error) {
	return gateCommandRisk(ctx, f.opts, f.kind, f.rawCommand, f.interpreter, targets)
}

// commandRiskDecision returns (reason, denied, warnings, err) for one target.
// Built-in critical signals deny first, then the guardrail floor, then the OPA
// enforcer (if any). The shared decision logic lives in cmdgate.Decide so the
// engine, web API, and MCP server all gate exec identically; this function only
// builds the recipe-context policy input and the guardrail Attrs.
func commandRiskDecision(ctx context.Context, enforcer *policy.Enforcer, rules *guardrails.Ruleset, actor, kind, rawCommand string, analysis commandrisk.Analysis, opts ExecutionOptions, t hosts.Record) (string, bool, []string, error) {
	input := map[string]any{
		"action": "command_exec",
		"actor":  actor,
		"command": map[string]any{
			"raw":          rawCommand,
			"riskSignals":  analysis.Signals,
			"detected":     analysis.Detected,
			"max_severity": string(analysis.MaxSeverity),
			"interpreter":  analysis.Interpreter,
		},
		"target": map[string]any{
			"name":      t.Name,
			"provider":  t.Provider,
			"env":       t.Meta["env"],
			"groups":    t.Groups,
			"host_vars": hostVarsForPolicy(t, opts.Inventory),
		},
		"execution": map[string]any{
			"recipe":  opts.Recipe.Name,
			"dry_run": !opts.Execute,
			"step":    kind,
		},
	}

	res, err := cmdgate.Decide(ctx, enforcer, rules, analysis, input, rawCommand,
		guardrails.Attrs{Provider: t.Provider, Groups: t.Groups, Name: t.Name})
	if err != nil {
		return "", false, nil, fmt.Errorf("command risk policy: %w", err)
	}
	return res.Reason, res.Denied, res.Warnings, nil
}
