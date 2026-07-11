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

	as, _ := step.(*cuetry.AIStep)
	var model string
	if as != nil && as.AI != nil && strings.TrimSpace(as.AI.Model) != "" {
		model = strings.TrimSpace(as.AI.Model)
	} else if m := strings.TrimSpace(os.Getenv("OPENAI_MODEL")); m != "" {
		model = "(from OPENAI_MODEL)"
	} else {
		model = aichat.DefaultModel + " (built-in default)"
	}

	_, _ = fmt.Fprintf(out, "step %d: kind=ai host=%q %s model=%s (requires OPENAI_API_KEY to execute; single local completion, no transcript aggregation)\n",
		i, step.Base().Host, base, model)
	WriteCueStepNotifyDryLine(out, step)

	if as != nil && as.AI != nil {
		preview := strings.TrimSpace(as.AI.Prompt)
		if as.Templated {
			preview = "(templated) " + preview
		}
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
	i, step, out := req.Index, req.Step, resCh
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

	res := runCueStepAINewExecute(ctx, opts, i, step)
	AnnotateCueStepResult(&res, i, step, cuetry.KindAI)
	out <- res
	ObserveRecipeStep(opts.Obs, cuetry.KindAI, stepStart, []HostExecResult{res}, 1)

	if !res.Success {
		return fmt.Errorf("%s", res.ErrMsg)
	}
	return nil
}

// runCueStepAINewExecute performs a single local LLM completion for the new
// ai: step — no transcript/history aggregation, unlike summarize:.
func runCueStepAINewExecute(ctx context.Context, opts ExecutionOptions, stepIdx int, step cuetry.Step) HostExecResult {
	stepNo := stepIdx + 1
	prefix := fmt.Sprintf("Step %d | ai", stepNo)
	as, _ := step.(*cuetry.AIStep)
	if as == nil || as.AI == nil {
		return HostExecResult{Name: prefix, Provider: "local", IP: "-", Success: false, ErrMsg: "internal: missing ai block"}
	}

	prompt := as.AI.Prompt
	if as.Templated {
		rendered, err := renderStepTemplate(prompt, opts, nil)
		if err != nil {
			return HostExecResult{Name: prefix, Provider: "local", IP: "-", Success: false, ErrMsg: err.Error()}
		}
		prompt = rendered
	}

	system := strings.TrimSpace(as.AI.SystemPrompt)
	if system == "" {
		system = "You are a helpful assistant."
	}
	model := strings.TrimSpace(as.AI.Model)
	maxTok := as.AI.MaxOutputTokens

	text, err := aichat.Complete(ctx, system, prompt, model, maxTok)
	if err != nil {
		return HostExecResult{Name: prefix, Provider: "local", IP: "-", Success: false, ErrMsg: err.Error()}
	}
	output := text
	if step.Base().NotifyEnabled() {
		output += CueStepNotifyAppendSuffix(ctx, opts.Recipe, stepNo, cuetry.KindAI, step.Base().Notify, text)
	}
	return HostExecResult{Name: prefix, Provider: "local", IP: "-", Success: true, Output: output}
}
