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
func gateCommandRisk(ctx context.Context, run *CueRun, kind, rawCommand, interpreter string, targets []hosts.Record) (allowed []hosts.Record, skipped []HostExecResult, err error) {
	if riskGateDisabled() || strings.TrimSpace(rawCommand) == "" {
		return targets, nil, nil
	}

	analysis := commandrisk.AnalyzeStep(rawCommand, interpreter)
	enforcer := run.Params.Enforcer
	actor := actorOrAPI(run.Params.ActorID)

	for _, t := range targets {
		reason, denied, evalErr := commandRiskDecision(ctx, enforcer, actor, kind, rawCommand, analysis, run, t)
		if evalErr != nil {
			return nil, nil, evalErr
		}
		if !denied {
			allowed = append(allowed, t)
			continue
		}
		sk := WhenSkippedResult(t)
		sk.Output = "(blocked: " + reason + ")"
		skipped = append(skipped, sk)
	}
	return allowed, skipped, nil
}

// riskStepFilter wraps gateCommandRisk as a StepFilter so the risk gate
// participates in the same pipeline interface as policyStepFilter and whenStepFilter.
type riskStepFilter struct {
	run         *CueRun
	kind        string
	rawCommand  string
	interpreter string
}

// NewRiskStepFilter returns a StepFilter that gates targets via the command
// risk analysis (built-in critical signals + OPA command_exec decision).
func NewRiskStepFilter(run *CueRun, kind, rawCommand, interpreter string) StepFilter {
	return &riskStepFilter{run: run, kind: kind, rawCommand: rawCommand, interpreter: interpreter}
}

func (f *riskStepFilter) Filter(ctx context.Context, targets []hosts.Record) ([]hosts.Record, []HostExecResult, error) {
	return gateCommandRisk(ctx, f.run, f.kind, f.rawCommand, f.interpreter, targets)
}

var _ StepFilter = (*riskStepFilter)(nil)

// commandRiskDecision returns (reason, denied, err) for one target. Built-in
// critical signals deny first; otherwise the OPA enforcer (if any) decides. The
// shared decision logic lives in cmdgate.Decide so the engine, web API, and MCP
// server all gate exec identically; this function only builds the recipe-context
// policy input.
func commandRiskDecision(ctx context.Context, enforcer *policy.Enforcer, actor, kind, rawCommand string, analysis commandrisk.Analysis, run *CueRun, t hosts.Record) (string, bool, error) {
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
			"host_vars": hostVarsForPolicy(t, run.Params.Inventory),
		},
		"execution": map[string]any{
			"recipe":  run.Params.Recipe.Name,
			"dry_run": !run.Params.Execute,
			"step":    kind,
		},
	}

	reason, denied, err := cmdgate.Decide(ctx, enforcer, analysis, input)
	if err != nil {
		return "", false, fmt.Errorf("command risk policy: %w", err)
	}
	return reason, denied, nil
}
