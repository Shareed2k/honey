package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/guardrails"
)

type planCommandInput struct {
	Command     string `json:"command"                mod:"trim" validate:"required"`
	Target      string `json:"target,omitempty"       mod:"trim"`
	Interpreter string `json:"interpreter,omitempty"  mod:"trim"`
}

type planCommandOutput struct {
	Decision string                   `json:"decision"` // allow | deny
	Risk     commandrisk.Severity     `json:"risk"`
	Signals  []commandrisk.RiskSignal `json:"signals"`
	Reason   string                   `json:"reason,omitempty"`
	Warnings []string                 `json:"warnings,omitempty"`
}

// handlePlanCommand evaluates command risk without executing it.
// Safe: no SSH dial, no state change. Returns risk analysis + policy decision.
func handlePlanCommand(ctx context.Context, _ *mcp.CallToolRequest, in planCommandInput) (*mcp.CallToolResult, planCommandOutput, error) {
	if err := conform.Struct(ctx, &in); err != nil {
		return nil, planCommandOutput{}, err
	}
	if err := validate.Struct(in); err != nil {
		return nil, planCommandOutput{}, fmt.Errorf("command: required")
	}

	analysis := commandrisk.AnalyzeStep(in.Command, in.Interpreter)

	input := map[string]any{
		"action": "command_exec",
		"actor":  mcpActor,
		"command": map[string]any{
			"raw":          in.Command,
			"interpreter":  in.Interpreter,
			"max_severity": string(analysis.MaxSeverity),
		},
		"target": map[string]any{
			"name": in.Target,
		},
	}

	res, err := cmdgate.Decide(ctx, policyEnforcer, guardrailRuleset, analysis, input, in.Command, guardrails.Attrs{Name: in.Target})
	if err != nil {
		return nil, planCommandOutput{}, fmt.Errorf("plan_command: policy eval: %w", err)
	}

	decision := "allow"
	if res.Denied {
		decision = "deny"
	}

	out := planCommandOutput{
		Decision: decision,
		Risk:     analysis.MaxSeverity,
		Signals:  analysis.Signals,
		Reason:   res.Reason,
		Warnings: res.Warnings,
	}
	if out.Signals == nil {
		out.Signals = []commandrisk.RiskSignal{}
	}
	return nil, out, nil
}
