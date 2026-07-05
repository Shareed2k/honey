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
	RegisterStepExecutor(cuetry.KindAI, &AIExecutor{})
}

// AIExecutor executes the corresponding recipe step.
type AIExecutor struct{}

// ExecuteDryRun executes a dry run of the step.
func (e *AIExecutor) ExecuteDryRun(_ context.Context, req ExecutionRequest, opts ExecutionOptions, out io.Writer) error {
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
	if as, ok := step.(*cuetry.AIStep); ok && as.AI != nil && strings.TrimSpace(as.AI.Model) != "" {
		model = strings.TrimSpace(as.AI.Model)
	} else if m := strings.TrimSpace(os.Getenv("OPENAI_MODEL")); m != "" {
		model = "(from OPENAI_MODEL)"
	} else {
		model = aichat.DefaultModel + " (built-in default)"
	}

	_, _ = fmt.Fprintf(out, "step %d: kind=ai host=%q %s model=%s (requires OPENAI_API_KEY to execute; summarizes all prior step outputs in one request)\n",
		i, step.Base().Host, base, model)
	WriteCueStepNotifyDryLine(out, step)

	ai, _ := step.(*cuetry.AIStep)
	if ai != nil && ai.AI != nil {
		preview := strings.TrimSpace(ai.AI.Prompt)
		if len(preview) > 120 {
			preview = preview[:119] + "…"
		}
		capture := ""
		if outName := strings.TrimSpace(step.Base().Output); outName != "" {
			capture = fmt.Sprintf(" capture=%q", outName)
		}
		_, _ = fmt.Fprintf(out, "step %d: kind=ai target=local%s prompt=%q\n", i, capture, preview)
	}

	return nil
}

// ExecuteStream streams the step execution.
func (e *AIExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	i, step, history, aiSystemPrompt, out := req.Index, req.Step, req.History, opts.AISystemPrompt, resCh

	stepStart := time.Now()
	kv := KvReaderFromCoordinator(opts.RecipeKV)
	ok, whenErr := EvalAIStepWhen(ctx, opts.Recipe, step, opts.OutputStore, opts.SecretResolver, kv, opts.CLIEnv, opts.Execute)
	if whenErr != nil {
		return whenErr
	}

	if !ok {
		res := HostExecResult{
			Name:     fmt.Sprintf("Step %d | ai", i+1),
			Provider: "local",
			Skipped:  true,
			Output:   "(skipped: when)",
		}
		AnnotateCueStepResult(&res, i, step, cuetry.KindAI)
		out <- res
		ObserveRecipeStep(opts.Obs, cuetry.KindAI, stepStart, []HostExecResult{res}, 1)
		return nil
	}

	res := RunCueStepAIExecute(ctx, opts.Recipe, i, step, history, aiSystemPrompt)
	AnnotateCueStepResult(&res, i, step, cuetry.KindAI)
	out <- res
	ObserveRecipeStep(opts.Obs, cuetry.KindAI, stepStart, []HostExecResult{res}, 1)

	if !res.Success {
		return fmt.Errorf("%s", res.ErrMsg)
	}
	return nil
}

// RunCueStepAIExecute performs the actual AI model completion based on recipe context.
func RunCueStepAIExecute(ctx context.Context, recipe cuetry.Recipe, stepIdx int, step cuetry.Step, history [][]HostExecResult, aiSystemPromptFromCfg string) HostExecResult {
	stepNo := stepIdx + 1
	prefix := fmt.Sprintf("Step %d | ai", stepNo)
	as, _ := step.(*cuetry.AIStep)
	if as == nil || as.AI == nil {
		return HostExecResult{Name: prefix, Provider: "local", IP: "-", Success: false, ErrMsg: "internal: missing ai block"}
	}
	ai := as.AI
	transcript := BuildCueRecipeTranscript(history)
	maxIn := CueAIDefaultMaxInputChars
	if ai.MaxInputChars > 0 {
		maxIn = ai.MaxInputChars
	}
	transcript = TruncateCueTranscript(transcript, maxIn)
	system := ai.ResolveSystemPrompt(aiSystemPromptFromCfg)
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
	output := text
	if step.Base().NotifyEnabled() {
		output += CueStepNotifyAppendSuffix(ctx, recipe, stepNo, cuetry.KindAI, step.Base().Notify, text)
	}
	return HostExecResult{
		Name:     prefix,
		Provider: "local",
		IP:       "-",
		Success:  true,
		Output:   output,
	}
}
