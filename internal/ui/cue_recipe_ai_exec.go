package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/aichat"
	"github.com/shareed2k/honey/internal/cuetry"
)

func runCueStepAIExecute(ctx context.Context, recipe cuetry.Recipe, stepIdx int, step cuetry.RecipeStep, history [][]HostExecResult, aiSystemPromptFromCfg string) HostExecResult {
	stepNo := stepIdx + 1
	prefix := fmt.Sprintf("Step %d | ai", stepNo)
	ai := step.AI
	if ai == nil {
		return HostExecResult{Name: prefix, Provider: "local", IP: "-", Success: false, ErrMsg: "internal: missing ai block"}
	}
	transcript := BuildCueRecipeTranscript(history)
	maxIn := cueAIDefaultMaxInputChars
	if ai.MaxInputChars > 0 {
		maxIn = ai.MaxInputChars
	}
	transcript = TruncateCueTranscript(transcript, maxIn)
	system := cuetry.ResolveRecipeAISystemPrompt(ai, aiSystemPromptFromCfg)
	userBody := strings.TrimSpace(ai.Prompt) + "\n\n--- Transcript ---\n" + transcript

	model := strings.TrimSpace(ai.Model)
	maxTok := ai.MaxOutputTokens

	text, err := aichat.Complete(ctx, system, userBody, model, maxTok)
	if err != nil {
		return HostExecResult{
			Name:     prefix,
			Provider: "local",
			IP:       "-",
			Success:  false,
			ErrMsg:   err.Error(),
		}
	}
	out := text
	if step.NotifyEnabled() {
		out += CueStepNotifyAppendSuffix(ctx, recipe, stepNo, cuetry.StepKindAI, step.Notify, text)
	}
	return HostExecResult{
		Name:     prefix,
		Provider: "local",
		IP:       "-",
		Success:  true,
		Output:   out,
	}
}

func runCueStepAIDry(out io.Writer, _ cuetry.Recipe, execute bool, i int, step cuetry.RecipeStep) error {
	if execute {
		return nil
	}
	base := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if base == "" {
		base = "(default https://api.openai.com/v1)"
	} else {
		base = "(OPENAI_BASE_URL set)"
	}
	var model string
	if m := strings.TrimSpace(step.AI.Model); m != "" {
		model = m
	} else if m := strings.TrimSpace(os.Getenv("OPENAI_MODEL")); m != "" {
		model = "(from OPENAI_MODEL)"
	} else {
		model = aichat.DefaultModel + " (built-in default)"
	}
	_, _ = fmt.Fprintf(out, "step %d: kind=ai host=%q %s model=%s (requires OPENAI_API_KEY to execute; summarizes all prior step outputs in one request)\n",
		i, step.Host, base, model)
	WriteCueStepNotifyDryLine(out, step)
	return nil
}
