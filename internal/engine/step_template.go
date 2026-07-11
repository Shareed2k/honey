package engine

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

func runCueStepTemplateOnHost(
	ctx context.Context,
	recipe cuetry.Recipe,
	stepIdx int,
	step cuetry.Step,
	tc TargetContext,
	outputStore *cuetry.StepOutputStore,
	outputCapture *cuetry.RecipeOutputCapture,
	recipeKV *RecipeKVCoordinator,
	_ cuetry.SecretResolver,
	execute bool,
) HostExecResult {
	stepNo := stepIdx + 1
	hostLabel := tc.Record.Name
	if strings.TrimSpace(hostLabel) == "" {
		hostLabel = cuetry.MatchLocalAIHost
	}
	prefix := fmt.Sprintf("Step %d | template | %s", stepNo, hostLabel)
	ts, _ := step.(*cuetry.TemplateStep)
	var tpl *cuetry.RecipeStepTemplate
	render := ""
	if ts != nil {
		tpl = ts.Template
		render = ts.Render
	}
	if tpl == nil && strings.TrimSpace(render) == "" {
		return HostExecResult{Name: prefix, Provider: tc.Record.Provider, IP: tc.Record.PrimaryIP, Success: false, ErrMsg: "internal: missing template block"}
	}
	templateBody := strings.TrimSpace(render)
	data := map[string]any{}
	if tpl != nil {
		templateBody = tpl.Template
		data = cloneTemplateData(tpl.Data)
	}
	mode, _ := cuetry.RecipeExecutionMode(recipe)
	hostName := hostLabel
	extraEnv := tc.Env
	if mode == cuetry.ExecutionModeGraph && len(step.Base().EnvFrom) > 0 {
		if err := cuetry.PrepareTemplateData(data, step.Base(), outputStore, outputCapture, KvReaderFromCoordinator(recipeKV), hostName, extraEnv, !execute, recipe.MatrixExpansions); err != nil {
			return HostExecResult{Name: prefix, Provider: tc.Record.Provider, IP: tc.Record.PrimaryIP, Success: false, ErrMsg: err.Error()}
		}
	} else if len(extraEnv) > 0 || outputCapture != nil {
		vars := cuetry.BuildRecipeVarMap(outputCapture, extraEnv)
		if err := cuetry.ExpandRecipeVarsInData(data, vars, execute); err != nil {
			return HostExecResult{Name: prefix, Provider: tc.Record.Provider, IP: tc.Record.PrimaryIP, Success: false, ErrMsg: err.Error()}
		}
		for k, v := range extraEnv {
			if _, ok := data[k]; !ok {
				data[k] = v
			}
		}
	}
	if outputCapture != nil {
		data["outputs"] = outputCapture.View()
	}
	rendered, err := cuetry.RenderTemplate(cuetry.RenderTemplateOpts{
		Template: templateBody,
		Data:     data,
		KV:       KvReaderFromCoordinator(recipeKV),
		Funcs:    cuetry.OutputTemplateFuncMap(outputCapture),
	})
	if err != nil {
		return HostExecResult{Name: prefix, Provider: tc.Record.Provider, IP: tc.Record.PrimaryIP, Success: false, ErrMsg: err.Error()}
	}
	recordTemplateCapture(recipe, step, tc.Record, outputStore, outputCapture, rendered)
	out := rendered
	if step.Base().NotifyEnabled() {
		out += CueStepNotifyAppendSuffix(ctx, recipe, stepNo, cuetry.KindTemplate, step.Base().Notify, rendered)
	}
	return HostExecResult{
		Name:          prefix,
		Provider:      tc.Record.Provider,
		IP:            tc.Record.PrimaryIP,
		Success:       true,
		Output:        out,
		OutputCapture: cuetry.StepOutputName(step),
	}
}

func init() {
	RegisterStepExecutor(cuetry.KindTemplate, &TemplateExecutor{})
}

// TemplateExecutor executes the corresponding recipe step.
type TemplateExecutor struct{}

// ExecuteDryRun executes a dry run of the step.
func (e *TemplateExecutor) ExecuteDryRun(_ context.Context, req ExecutionRequest, opts ExecutionOptions, out io.Writer) error {
	out, execute, i, step := out, opts.Execute, req.Index, req.Step
	if execute {
		return nil
	}
	ts, _ := step.(*cuetry.TemplateStep)
	var tpl *cuetry.RecipeStepTemplate
	render := ""
	if ts != nil {
		tpl = ts.Template
		render = ts.Render
	}
	preview := ""
	capture := ""
	if strings.TrimSpace(render) != "" {
		preview = strings.TrimSpace(render)
		if outName := strings.TrimSpace(step.Base().Output); outName != "" {
			capture = fmt.Sprintf(" capture=%q", outName)
		}
	} else if tpl != nil {
		preview = strings.TrimSpace(tpl.Template)
		if len(preview) > 120 {
			preview = preview[:119] + "…"
		}
		if outName := strings.TrimSpace(tpl.Output); outName != "" {
			capture = fmt.Sprintf(" capture=%q", outName)
		}
	}
	_, _ = fmt.Fprintf(out, "step %d: kind=template host=%q%s preview=%q (Go text/template; ${VAR} expanded in data only)\n",
		i, step.Base().Host, capture, preview)
	WriteCueStepNotifyDryLine(out, step)
	return nil
}

// ExecuteStream streams the step execution.
func (e *TemplateExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	stepIdx, step, ch := req.Index, req.Step, resCh
	targets := req.Targets

	if len(targets) == 0 {
		return nil
	}

	maxConc := RecipeHostMaxConc(step, opts.Recipe.Defaults)
	var mu sync.Mutex
	var stepErr error

	DispatchHostResults(ctx, targets, maxConc, 8, func(target TargetContext) HostExecResult {
		return runCueStepTemplateOnHost(
			ctx,
			opts.Recipe,
			stepIdx,
			step,
			target,
			opts.OutputStore,
			opts.OutputCapture,
			opts.RecipeKV,
			opts.SecretResolver,
			opts.Execute,
		)
	}, func(res HostExecResult) {
		if !res.Success && !res.Skipped {
			mu.Lock()
			stepErr = fmt.Errorf("template failed on %s: %s", res.Name, res.ErrMsg)
			mu.Unlock()
		}
		if ch != nil {
			ch <- res
		}
	})
	return stepErr
}

func recordTemplateCapture(
	recipe cuetry.Recipe,
	step cuetry.Step,
	target hosts.Record,
	outputStore *cuetry.StepOutputStore,
	outputCapture *cuetry.RecipeOutputCapture,
	stdout string,
) {
	mode, _ := cuetry.RecipeExecutionMode(recipe)
	if mode != cuetry.ExecutionModeGraph {
		return
	}
	id := strings.TrimSpace(step.Base().ID)
	hostName := strings.TrimSpace(target.Name)
	if hostName == "" {
		hostName = cuetry.MatchLocalAIHost
	}
	if id != "" && outputStore != nil {
		outputStore.Record(id, hostName, stdout)
	}
	if strings.TrimSpace(step.Base().Host) == cuetry.MatchLocalAIHost && outputCapture != nil {
		if name := cuetry.StepOutputName(step); name != "" {
			outputCapture.Set(name, stdout)
		}
	}
}

func cloneTemplateData(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
