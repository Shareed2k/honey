package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/aichat"
	"github.com/shareed2k/honey/internal/cuetry"
)

func init() {
	RegisterStepExecutor(cuetry.KindSummarize, &SummarizeExecutor{})
}

// SummarizeExecutor executes the corresponding recipe step.
type SummarizeExecutor struct{}

// ExecuteDryRun executes a dry run of the step.
func (e *SummarizeExecutor) ExecuteDryRun(_ context.Context, req ExecutionRequest, opts ExecutionOptions, out io.Writer) error {
	out, execute, i, step := out, opts.Execute, req.Index, req.Step
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
	if ss, ok := step.(*cuetry.SummarizeStep); ok && ss.Summarize != nil && strings.TrimSpace(ss.Summarize.Model) != "" {
		model = strings.TrimSpace(ss.Summarize.Model)
	} else if m := strings.TrimSpace(os.Getenv("OPENAI_MODEL")); m != "" {
		model = "(from OPENAI_MODEL)"
	} else {
		model = aichat.DefaultModel + " (built-in default)"
	}

	_, _ = fmt.Fprintf(out, "step %d: kind=summarize host=%q %s model=%s (requires OPENAI_API_KEY to execute; summarizes all prior step outputs in one request)\n",
		i, step.Base().Host, base, model)
	WriteCueStepNotifyDryLine(out, step)

	ss, _ := step.(*cuetry.SummarizeStep)
	if ss != nil && ss.Summarize != nil {
		preview := strings.TrimSpace(ss.Summarize.Prompt)
		if len(preview) > 120 {
			preview = preview[:119] + "…"
		}
		capture := ""
		if outName := strings.TrimSpace(step.Base().Output); outName != "" {
			capture = fmt.Sprintf(" capture=%q", outName)
		}
		_, _ = fmt.Fprintf(out, "step %d: kind=summarize target=local%s prompt=%q\n", i, capture, preview)
	}

	return nil
}

// ExecuteStream streams the step execution.
func (e *SummarizeExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	i, step, history, aiSystemPrompt, out := req.Index, req.Step, req.History, opts.AISystemPrompt, resCh

	stepStart := time.Now()
	kv := KvReaderFromCoordinator(opts.RecipeKV)
	ok, whenErr := EvalAIStepWhen(ctx, opts.Recipe, step, opts.OutputStore, opts.SecretResolver, kv, opts.CLIEnv, opts.Execute)
	if whenErr != nil {
		return whenErr
	}

	if !ok {
		res := HostExecResult{
			Name:     fmt.Sprintf("Step %d | summarize", i+1),
			Provider: "local",
			Skipped:  true,
			Output:   "(skipped: when)",
		}
		AnnotateCueStepResult(&res, i, step, cuetry.KindSummarize)
		out <- res
		ObserveRecipeStep(opts.Obs, cuetry.KindSummarize, stepStart, []HostExecResult{res}, 1)
		return nil
	}

	res := RunCueStepSummarizeExecute(ctx, opts.Recipe, i, step, history, aiSystemPrompt)
	AnnotateCueStepResult(&res, i, step, cuetry.KindSummarize)
	out <- res
	ObserveRecipeStep(opts.Obs, cuetry.KindSummarize, stepStart, []HostExecResult{res}, 1)

	if !res.Success {
		return fmt.Errorf("%s", res.ErrMsg)
	}
	return nil
}

// RunCueStepSummarizeExecute performs the actual AI model completion based on recipe context.
func RunCueStepSummarizeExecute(ctx context.Context, recipe cuetry.Recipe, stepIdx int, step cuetry.Step, history [][]HostExecResult, aiSystemPromptFromCfg string) HostExecResult {
	stepNo := stepIdx + 1
	prefix := fmt.Sprintf("Step %d | summarize", stepNo)
	ss, _ := step.(*cuetry.SummarizeStep)
	if ss == nil || ss.Summarize == nil {
		return HostExecResult{Name: prefix, Provider: "local", IP: "-", Success: false, ErrMsg: "internal: missing summarize block"}
	}
	sm := ss.Summarize
	transcript := BuildCueRecipeTranscript(history)
	maxIn := CueAIDefaultMaxInputChars
	if sm.MaxInputChars > 0 {
		maxIn = sm.MaxInputChars
	}
	transcript = TruncateCueTranscript(transcript, maxIn)
	system := sm.ResolveSystemPrompt(aiSystemPromptFromCfg)
	userBody := strings.TrimSpace(sm.Prompt) + "\n\n--- Transcript ---\n" + transcript

	model := strings.TrimSpace(sm.Model)
	maxTok := sm.MaxOutputTokens

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
	output := text
	if step.Base().NotifyEnabled() {
		output += CueStepNotifyAppendSuffix(ctx, recipe, stepNo, cuetry.KindSummarize, step.Base().Notify, text)
	}
	return HostExecResult{
		Name:     prefix,
		Provider: "local",
		IP:       "-",
		Success:  true,
		Output:   output,
	}
}
