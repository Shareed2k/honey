package cuetry

import (
	"fmt"
	"strings"
)

// StepSummary is a host-agnostic one-line summary of a recipe step.
// It backs the wizard's Plan view and any other UI that wants a per-step
// digest without resolving target hosts.
type StepSummary struct {
	Index   int      `json:"index"`
	ID      string   `json:"id,omitempty"`
	Depends []string `json:"depends,omitempty"`
	Wave    int      `json:"wave,omitempty"`
	Kind    string   `json:"kind"`
	Host    string   `json:"host"`
	RunAs   string   `json:"run_as,omitempty"`
	When    string   `json:"when,omitempty"`
	Retry   string   `json:"retry,omitempty"`
	Notify  bool     `json:"notify,omitempty"`
	Preview string   `json:"preview"`
}

// RenderDryRunPlan returns a host-agnostic plan summary for r: one line per
// step plus a structured per-step list. It does not expand step.host against
// records, so callers can validate Recipe structure before any host resolution.
// The line format mirrors the per-target dry-run text in internal/ui — same
// "step N: kind=… host=… run_as=… preview=…" shape, minus per-host detail.
func RenderDryRunPlan(r Recipe) (string, []StepSummary, error) {
	mode, err := RecipeExecutionMode(r)
	if err != nil {
		return "", nil, err
	}
	var waveOf map[int]int
	if mode == ExecutionModeGraph {
		sg, gerr := BuildStepGraph(r.Steps)
		if gerr != nil {
			return "", nil, gerr
		}
		waveOf = make(map[int]int, len(r.Steps))
		for w, wave := range sg.Waves {
			for _, idx := range wave {
				waveOf[idx] = w + 1
			}
		}
	}
	steps := make([]StepSummary, 0, len(r.Steps))
	var b strings.Builder
	if mode == ExecutionModeGraph {
		if text, werr := FormatGraphWavesText(r); werr == nil {
			b.WriteString(text)
		}
	}
	for i, w := range r.Steps {
		sb := w.Step.Base()
		kindLabel := w.Step.Kind()
		runAs := EffectiveRunAs(sb, r.Defaults)
		preview := previewForStep(w.Step)
		summary := StepSummary{
			Index:   i,
			ID:      strings.TrimSpace(sb.ID),
			Depends: append([]string(nil), sb.Depends...),
			Kind:    kindLabel,
			Host:    strings.TrimSpace(sb.Host),
			RunAs:   runAs,
			When:    strings.TrimSpace(sb.When),
			Retry:   retrySummary(sb, r.Defaults),
			Notify:  sb.NotifyEnabled(),
			Preview: preview,
		}
		if waveOf != nil {
			summary.Wave = waveOf[i]
		}
		steps = append(steps, summary)
		whenPart := ""
		if summary.When != "" {
			whenPart = fmt.Sprintf(" when=%q", summary.When)
		}
		retryPart := ""
		if summary.Retry != "" {
			retryPart = " retry=" + summary.Retry
		}
		notifyPart := ""
		if summary.Notify {
			notifyPart = " notify=yes"
		}
		extras := whenPart + retryPart + notifyPart
		if summary.ID != "" {
			fmt.Fprintf(&b, "step %d (id=%q wave=%d depends=%v): kind=%s host=%q run_as=%q%s preview=%q\n",
				i, summary.ID, summary.Wave, summary.Depends, kindLabel, summary.Host, runAs, extras, preview)
		} else {
			fmt.Fprintf(&b, "step %d: kind=%s host=%q run_as=%q%s preview=%q\n",
				i, kindLabel, summary.Host, runAs, extras, preview)
		}
	}
	return b.String(), steps, nil
}

// previewForStep is a small, stable, host-agnostic one-line description of a
// step's action. It must never include secret values (env, tokens, etc.).
func previewForStep(s Step) string {
	const maxPreviewBytes = 160
	var p string
	switch v := s.(type) {
	case *CommandStep:
		p = strings.TrimSpace(v.Command)
	case *PutStep:
		if v.Put != nil {
			p = fmt.Sprintf("put %q -> remote:%q", strings.TrimSpace(v.Put.Local), strings.TrimSpace(v.Put.Remote))
		}
	case *GetStep:
		if v.Get != nil {
			p = fmt.Sprintf("get remote:%q -> %q", strings.TrimSpace(v.Get.Remote), strings.TrimSpace(v.Get.Local))
		}
	case *ScriptStep:
		if v.Script != nil {
			p = fmt.Sprintf("script %q -> remote:%q", strings.TrimSpace(v.Script.Local), strings.TrimSpace(v.Script.Remote))
		}
	case *AgentTransferStep:
		if v.AgentTransfer != nil {
			p = fmt.Sprintf("agent_transfer %s:%s -> %s:%s",
				strings.TrimSpace(v.Host), strings.TrimSpace(v.AgentTransfer.SourcePath),
				strings.TrimSpace(v.AgentTransfer.DestHost), strings.TrimSpace(v.AgentTransfer.DestPath))
		}
	case *AIStep:
		if v.AI != nil {
			p = "ai: " + strings.TrimSpace(v.AI.Prompt)
		}
	case *TemplateStep:
		if v.Template != nil {
			p = strings.TrimSpace(v.Template.Template)
			if out := strings.TrimSpace(v.Template.Output); out != "" {
				p = fmt.Sprintf("capture %q: %s", out, p)
			}
		}
	case *PluginStep:
		if v.Plugin != nil {
			p = fmt.Sprintf("plugin %s action=%s", strings.TrimSpace(v.Plugin.ID), strings.TrimSpace(v.Plugin.Action))
		}
	case *TunnelStep:
		if v.Tunnel != nil {
			t := v.Tunnel
			host := strings.TrimSpace(t.RemoteHost)
			if host == "" {
				host = "localhost"
			}
			mode := EffectiveTunnelMode(t)
			p = fmt.Sprintf("tunnel %s -> %s:%d", mode, host, t.RemotePort)
		}
	}
	p = strings.ReplaceAll(p, "\n", " ")
	if len(p) > maxPreviewBytes {
		p = p[:maxPreviewBytes-1] + "…"
	}
	return p
}

// retrySummary returns a compact per-step retry descriptor for plan UIs (no secrets).
func retrySummary(step *StepBase, defaults *RecipeDefaults) string {
	if step.Retry == nil {
		return ""
	}
	r := EffectiveRetry(step, defaults)
	if !r.Enabled() {
		return ""
	}
	return fmt.Sprintf("attempts=%d backoff=%s", r.Attempts, r.Backoff)
}
