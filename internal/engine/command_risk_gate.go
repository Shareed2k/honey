package engine

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
)

// riskDisableEnv, when set to a truthy value, bypasses the command risk gate
// entirely, including the OPA command_exec decision. Escape hatch for trusted
// runs.
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
// it may run (unless HONEY_RISK_DISABLE bypasses the gate entirely). When an OPA
// enforcer is configured it makes the contextual decision via the "command_exec"
// action, with the command-risk analysis included as data; require_approval /
// require_biometric verdicts deny with a clear reason in this non-interactive
// path (the approval flow is a later phase). With no enforcer configured,
// nothing is denied — OPA is honey's only command-authorization gate. Returns
// the allowed targets and skip results for denied ones — mirroring
// filterTargetsByPolicy so denied hosts stay visible in the run output.
func gateCommandRisk(ctx context.Context, opts ExecutionOptions, kind, rawCommand, interpreter string, targets []TargetContext) (allowed []TargetContext, skipped []HostExecResult, err error) {
	if riskGateDisabled() || strings.TrimSpace(rawCommand) == "" {
		return targets, nil, nil
	}

	analysis := commandrisk.AnalyzeStep(rawCommand, interpreter)
	enforcer := opts.Enforcer
	actor := actorOrAPI(opts.ActorID)

	for _, t := range targets {
		reason, denied, evalErr := commandRiskDecision(ctx, enforcer, actor, kind, rawCommand, analysis, opts, t.Record)
		if evalErr != nil {
			return nil, nil, evalErr
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
// risk analysis (data) plus the OPA command_exec decision.
func NewRiskStepFilter(opts ExecutionOptions, kind, rawCommand, interpreter string) *RiskStepFilter {
	return &RiskStepFilter{opts: opts, kind: kind, rawCommand: rawCommand, interpreter: interpreter}
}

// Filter applies the command risk gate to the given targets.
func (f *RiskStepFilter) Filter(ctx context.Context, targets []TargetContext) ([]TargetContext, []HostExecResult, error) {
	return gateCommandRisk(ctx, f.opts, f.kind, f.rawCommand, f.interpreter, targets)
}

// commandRiskDecision returns (reason, denied, err) for one target, deciding
// via the OPA enforcer (if any) — the shared decision logic lives in
// cmdgate.Decide so the engine, web API, and MCP server all gate exec
// identically. This function builds the recipe-context policy input, folding
// the command-risk analysis in as data (input.command.max_severity/
// riskSignals/detected) for a rego policy to act on.
func commandRiskDecision(ctx context.Context, enforcer *policy.Enforcer, actor, kind, rawCommand string, analysis commandrisk.Analysis, opts ExecutionOptions, t hosts.Record) (string, bool, error) {
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

	res, err := cmdgate.Decide(ctx, enforcer, input)
	if err != nil {
		return "", false, fmt.Errorf("command risk policy: %w", err)
	}
	return res.Reason, res.Denied, nil
}
