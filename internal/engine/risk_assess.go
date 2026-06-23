package engine

import (
	"context"
	"os"
	"path/filepath"

	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/policy"
)

// StepRisk is the risk assessment of one command/script step, for dry-run review.
type StepRisk struct {
	StepIndex int                  `json:"step_index"`
	Kind      string               `json:"kind"`
	Host      string               `json:"host,omitempty"`
	Command   string               `json:"command,omitempty"`
	Analysis  commandrisk.Analysis `json:"analysis"`
	Decision  *policy.Decision     `json:"decision,omitempty"`
}

// AssessCommandRisk analyzes every command/script step in the request's recipe
// and returns a per-step risk assessment for review (no execution). When an OPA
// enforcer is configured it also evaluates the command_exec decision against the
// first target record as a representative context.
func (r *RecipeRunner) AssessCommandRisk(ctx context.Context, req RunRequest) []StepRisk {
	var out []StepRisk
	for i, wrapper := range req.Recipe.Steps {
		step := wrapper.Step
		if step == nil {
			continue
		}
		command, ok := commandTextForRisk(step, req.RecipeDir)
		if !ok {
			continue
		}
		analysis := commandrisk.Analyze(command)
		sr := StepRisk{
			StepIndex: i,
			Kind:      step.Kind(),
			Host:      step.Base().Host,
			Command:   command,
			Analysis:  analysis,
		}
		if d := r.assessStepDecision(ctx, command, analysis, req); d != nil {
			sr.Decision = d
		}
		out = append(out, sr)
	}
	return out
}

// assessStepDecision evaluates the command_exec policy for a dry-run review,
// using the first record as representative context. Returns nil when no enforcer.
func (r *RecipeRunner) assessStepDecision(ctx context.Context, command string, a commandrisk.Analysis, req RunRequest) *policy.Decision {
	if r.opts.Enforcer == nil {
		return nil
	}
	target := map[string]any{}
	if len(req.Records) > 0 {
		t := req.Records[0]
		target = map[string]any{"name": t.Name, "provider": t.Provider, "env": t.Meta["env"], "groups": t.Groups}
	}
	d, err := r.opts.Enforcer.Evaluate(ctx, map[string]any{
		"action": "command_exec",
		"actor":  actorOrAPI(req.ActorID),
		"command": map[string]any{
			"raw": command, "riskSignals": a.Signals, "detected": a.Detected, "max_severity": string(a.MaxSeverity),
		},
		"target":    target,
		"execution": map[string]any{"recipe": req.Recipe.Name, "dry_run": true},
	})
	if err != nil {
		return nil
	}
	return &d
}

// commandTextForRisk returns the shell text to analyze for a step, and whether
// the step is shell-bearing (command or script). Script bodies are read from the
// local file relative to the recipe dir (best-effort).
func commandTextForRisk(step cuetry.Step, recipeDir string) (string, bool) {
	switch s := step.(type) {
	case *cuetry.CommandStep:
		return s.Command, true
	case *cuetry.ScriptStep:
		if s.Script == nil {
			return "", true
		}
		path := s.Script.Local
		if !filepath.IsAbs(path) && recipeDir != "" {
			path = filepath.Join(recipeDir, path)
		}
		// #nosec G304 -- recipe-author script path within the recipe dir.
		b, err := os.ReadFile(path)
		if err != nil {
			return "", true
		}
		return string(b), true
	default:
		return "", false
	}
}
