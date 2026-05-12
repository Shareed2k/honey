package cuetry

import (
	"fmt"
	"strings"
)

// StepSummary is a host-agnostic one-line summary of a recipe step.
// It backs the wizard's Plan view and any other UI that wants a per-step
// digest without resolving target hosts.
type StepSummary struct {
	Index   int    `json:"index"`
	Kind    string `json:"kind"`
	Host    string `json:"host"`
	RunAs   string `json:"run_as,omitempty"`
	Preview string `json:"preview"`
}

// RenderDryRunPlan returns a host-agnostic plan summary for r: one line per
// step plus a structured per-step list. It does not expand step.host against
// records, so callers can validate Recipe structure before any host resolution.
// The line format mirrors the per-target dry-run text in internal/ui — same
// "step N: kind=… host=… run_as=… preview=…" shape, minus per-host detail.
func RenderDryRunPlan(r Recipe) (string, []StepSummary, error) {
	steps := make([]StepSummary, 0, len(r.Steps))
	var b strings.Builder
	for i, step := range r.Steps {
		kind, err := ClassifyStep(step)
		if err != nil {
			return "", nil, fmt.Errorf("step %d: %w", i, err)
		}
		kindLabel := StepKindLabel(kind)
		runAs := EffectiveRunAs(step, r.Defaults)
		preview := previewForStep(kind, step)
		summary := StepSummary{
			Index:   i,
			Kind:    kindLabel,
			Host:    strings.TrimSpace(step.Host),
			RunAs:   runAs,
			Preview: preview,
		}
		steps = append(steps, summary)
		fmt.Fprintf(&b, "step %d: kind=%s host=%q run_as=%q preview=%q\n",
			i, kindLabel, summary.Host, runAs, preview)
	}
	return b.String(), steps, nil
}

// previewForStep is a small, stable, host-agnostic one-line description of a
// step's action. It must never include secret values (env, tokens, etc.).
func previewForStep(kind StepKind, s RecipeStep) string {
	const maxPreviewBytes = 160
	var p string
	switch kind {
	case StepKindCommand:
		p = strings.TrimSpace(s.Command)
	case StepKindPut:
		if s.Put != nil {
			p = fmt.Sprintf("put %q -> remote:%q", strings.TrimSpace(s.Put.Local), strings.TrimSpace(s.Put.Remote))
		}
	case StepKindGet:
		if s.Get != nil {
			p = fmt.Sprintf("get remote:%q -> %q", strings.TrimSpace(s.Get.Remote), strings.TrimSpace(s.Get.Local))
		}
	case StepKindScript:
		if s.Script != nil {
			p = fmt.Sprintf("script %q -> remote:%q", strings.TrimSpace(s.Script.Local), strings.TrimSpace(s.Script.Remote))
		}
	case StepKindAgentTransfer:
		if s.AgentTransfer != nil {
			p = fmt.Sprintf("agent_transfer %s:%s -> %s:%s",
				strings.TrimSpace(s.Host), strings.TrimSpace(s.AgentTransfer.SourcePath),
				strings.TrimSpace(s.AgentTransfer.DestHost), strings.TrimSpace(s.AgentTransfer.DestPath))
		}
	case StepKindAI:
		if s.AI != nil {
			p = "ai: " + strings.TrimSpace(s.AI.Prompt)
		}
	}
	p = strings.ReplaceAll(p, "\n", " ")
	if len(p) > maxPreviewBytes {
		p = p[:maxPreviewBytes-1] + "…"
	}
	return p
}
