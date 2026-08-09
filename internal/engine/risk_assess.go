package engine

import (
	"context"
	"path/filepath"

	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/guardrails"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/safepath"
)

// StepRisk is the risk assessment of one command/script step, for dry-run review.
type StepRisk struct {
	StepIndex   int                  `json:"step_index"`
	Kind        string               `json:"kind"`
	Host        string               `json:"host,omitempty"`
	Command     string               `json:"command,omitempty"`
	Interpreter string               `json:"interpreter,omitempty"`
	Analysis    commandrisk.Analysis `json:"analysis"`
	Decision    *policy.Decision     `json:"decision,omitempty"`
	Warnings    []string             `json:"warnings,omitempty"`
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
		command, interpreter, ok := commandTextForRisk(step, req.RecipeDir)
		if !ok {
			continue
		}
		analysis := commandrisk.AnalyzeStep(command, interpreter)
		sr := StepRisk{
			StepIndex:   i,
			Kind:        step.Kind(),
			Host:        step.Base().Host,
			Command:     command,
			Interpreter: interpreter,
			Analysis:    analysis,
		}
		if d, warnings := r.assessStepDecision(ctx, command, interpreter, req); d != nil || len(warnings) > 0 {
			sr.Decision = d
			sr.Warnings = warnings
		}
		out = append(out, sr)
	}
	return out
}

// assessStepDecision evaluates the command_exec gate for a dry-run review via
// cmdgate.AssessTargets (summaryOnly=true, first record as representative).
// This ensures critical-signal hard-denies and guardrail-deny rules are applied
// even in preview mode, giving the same verdict the runtime gate would produce.
// It returns the deny decision (nil when allowed) and any guardrail warnings.
func (r *RecipeRunner) assessStepDecision(ctx context.Context, command, interpreter string, req RunRequest) (*policy.Decision, []string) {
	var inputs []cmdgate.TargetInput
	if len(req.Records) > 0 {
		t := req.Records[0]
		inputs = []cmdgate.TargetInput{{
			Name:  t.Name,
			Attrs: guardrails.Attrs{Provider: t.Provider, Groups: t.Groups, Name: t.Name},
			PolicyInput: map[string]any{
				"action": "command_exec",
				"actor":  actorOrAPI(req.ActorID),
				"command": map[string]any{
					"raw": command, "interpreter": interpreter,
				},
				"target":    map[string]any{"name": t.Name, "provider": t.Provider, "env": t.Meta["env"], "groups": t.Groups},
				"execution": map[string]any{"recipe": req.Recipe.Name, "dry_run": true},
			},
		}}
	}
	_, decisions, err := cmdgate.AssessTargets(ctx, r.opts.Enforcer, r.opts.Guardrails, command, interpreter, inputs, true)
	if err != nil || len(decisions) == 0 {
		return nil, nil
	}
	d := decisions[0]
	if !d.Denied {
		return nil, d.Warnings
	}
	dec := policy.Decision{Allow: false, DenyReason: d.Reason, Decision: "deny"}
	return &dec, d.Warnings
}

// commandTextForRisk returns the text to analyze for a step, its interpreter,
// and whether the step is command-bearing (command or script). Script bodies are
// read from the local file relative to the recipe dir (best-effort).
func commandTextForRisk(step cuetry.Step, recipeDir string) (command, interpreter string, ok bool) {
	switch s := step.(type) {
	case *cuetry.CommandStep:
		return s.Command, s.Interpreter, true
	case *cuetry.ScriptStep:
		if s.Script == nil {
			return "", s.Interpreter, true
		}
		path := s.Script.Local
		if !filepath.IsAbs(path) && recipeDir != "" {
			// Constrain a relative script path to the recipe dir (reject "../" escapes).
			joined, err := safepath.JoinUnder(recipeDir, path)
			if err != nil {
				return "", s.Interpreter, true
			}
			path = joined
		}
		// safepath.ReadFile reads via os.Root on the parent dir, so the basename
		// cannot traverse — no G304 suppression needed.
		b, err := safepath.ReadFile(path)
		if err != nil {
			return "", s.Interpreter, true
		}
		return string(b), s.Interpreter, true
	default:
		return "", "", false
	}
}
