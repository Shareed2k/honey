package ui

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
	step cuetry.RecipeStep,
	target hosts.Record,
	outputStore *cuetry.StepOutputStore,
	outputCapture *cuetry.RecipeOutputCapture,
	recipeKV *RecipeKVCoordinator,
	secretResolver cuetry.SecretResolver,
	execute bool,
) HostExecResult {
	stepNo := stepIdx + 1
	hostLabel := target.Name
	if strings.TrimSpace(hostLabel) == "" {
		hostLabel = cuetry.MatchLocalAIHost
	}
	prefix := fmt.Sprintf("Step %d | template | %s", stepNo, hostLabel)
	tpl := step.Template
	if tpl == nil {
		return HostExecResult{Name: prefix, Provider: target.Provider, IP: target.PrimaryIP, Success: false, ErrMsg: "internal: missing template block"}
	}
	data := cloneTemplateData(tpl.Data)
	mode, _ := cuetry.RecipeExecutionMode(recipe)
	hostName := hostLabel
	extraEnv := make(map[string]string)
	if len(step.Env) > 0 || len(step.Secrets) > 0 {
		env, err := cuetry.EffectiveEnvForRun(ctx, execute, secretResolver, step, recipe.Defaults, nil, &target)
		if err != nil {
			return HostExecResult{Name: prefix, Provider: target.Provider, IP: target.PrimaryIP, Success: false, ErrMsg: err.Error()}
		}
		extraEnv = env
	}
	if mode == cuetry.ExecutionModeGraph && len(step.EnvFrom) > 0 {
		if err := cuetry.PrepareTemplateData(data, step, outputStore, outputCapture, kvReaderFromCoordinator(recipeKV), hostName, extraEnv, !execute); err != nil {
			return HostExecResult{Name: prefix, Provider: target.Provider, IP: target.PrimaryIP, Success: false, ErrMsg: err.Error()}
		}
	} else if len(extraEnv) > 0 || outputCapture != nil {
		vars := cuetry.BuildRecipeVarMap(outputCapture, extraEnv)
		if err := cuetry.ExpandRecipeVarsInData(data, vars, execute); err != nil {
			return HostExecResult{Name: prefix, Provider: target.Provider, IP: target.PrimaryIP, Success: false, ErrMsg: err.Error()}
		}
		for k, v := range extraEnv {
			if _, ok := data[k]; !ok {
				data[k] = v
			}
		}
	}
	rendered, err := cuetry.RenderTemplate(cuetry.RenderTemplateOpts{
		Template: tpl.Template,
		Data:     data,
		KV:       kvReaderFromCoordinator(recipeKV),
	})
	if err != nil {
		return HostExecResult{Name: prefix, Provider: target.Provider, IP: target.PrimaryIP, Success: false, ErrMsg: err.Error()}
	}
	recordTemplateCapture(recipe, step, target, outputStore, outputCapture, rendered)
	out := rendered
	if step.NotifyEnabled() {
		out += CueStepNotifyAppendSuffix(ctx, recipe, stepNo, cuetry.StepKindTemplate, step.Notify, rendered)
	}
	return HostExecResult{
		Name:     prefix,
		Provider: target.Provider,
		IP:       target.PrimaryIP,
		Success:  true,
		Output:   out,
	}
}

func streamCueTemplateStep(
	ctx context.Context,
	recipe cuetry.Recipe,
	recipeDir string,
	stepIdx int,
	step cuetry.RecipeStep,
	records []hosts.Record,
	outputStore *cuetry.StepOutputStore,
	outputCapture *cuetry.RecipeOutputCapture,
	recipeKV *RecipeKVCoordinator,
	secretResolver cuetry.SecretResolver,
	execute bool,
	out chan<- HostExecResult,
) ([]HostExecResult, error) {
	_ = recipeDir
	targets, err := cuetry.ExpandStepHosts(step.Host, records)
	if err != nil {
		return nil, fmt.Errorf("step %d: %w", stepIdx, err)
	}
	prog, err := compileStepWhen(step)
	if err != nil {
		return nil, err
	}
	kv := kvReaderFromCoordinator(recipeKV)
	var kept []hosts.Record
	var skipped []HostExecResult
	for _, t := range targets {
		if prog != nil {
			ok, err := evalStepWhen(ctx, prog, recipe, step, t, nil, outputStore, secretResolver, kv, execute)
			if err != nil {
				return nil, err
			}
			if !ok {
				skipped = append(skipped, whenSkippedResult(t))
				continue
			}
		}
		kept = append(kept, t)
	}
	var rows []HostExecResult
	rows = append(rows, skipped...)
	if len(kept) == 0 {
		return rows, nil
	}
	maxConc := recipeHostMaxConc(step, recipe.Defaults)
	if maxConc <= 0 {
		maxConc = 8
	}
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var stepErr error
	for _, target := range kept {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := runCueStepTemplateOnHost(ctx, recipe, stepIdx, step, target, outputStore, outputCapture, recipeKV, secretResolver, execute)
			mu.Lock()
			rows = append(rows, res)
			if !res.Success && !res.Skipped {
				stepErr = fmt.Errorf("template failed on %s: %s", target.Name, res.ErrMsg)
			}
			mu.Unlock()
			if out != nil {
				out <- res
			}
		}()
	}
	wg.Wait()
	return rows, stepErr
}

func runCueStepTemplateDry(out io.Writer, execute bool, i int, step cuetry.RecipeStep) error {
	if execute {
		return nil
	}
	tpl := step.Template
	preview := ""
	capture := ""
	if tpl != nil {
		preview = strings.TrimSpace(tpl.Template)
		if len(preview) > 120 {
			preview = preview[:119] + "…"
		}
		if outName := strings.TrimSpace(tpl.Output); outName != "" {
			capture = fmt.Sprintf(" capture=%q", outName)
		}
	}
	_, _ = fmt.Fprintf(out, "step %d: kind=template host=%q%s preview=%q (Go text/template; ${VAR} expanded in data only)\n",
		i, step.Host, capture, preview)
	WriteCueStepNotifyDryLine(out, step)
	return nil
}

func recordTemplateCapture(
	recipe cuetry.Recipe,
	step cuetry.RecipeStep,
	target hosts.Record,
	outputStore *cuetry.StepOutputStore,
	outputCapture *cuetry.RecipeOutputCapture,
	stdout string,
) {
	mode, _ := cuetry.RecipeExecutionMode(recipe)
	if mode != cuetry.ExecutionModeGraph {
		return
	}
	id := strings.TrimSpace(step.ID)
	hostName := strings.TrimSpace(target.Name)
	if hostName == "" {
		hostName = cuetry.MatchLocalAIHost
	}
	if id != "" && outputStore != nil {
		outputStore.Record(id, hostName, stdout)
	}
	if step.Template != nil && strings.TrimSpace(step.Host) == cuetry.MatchLocalAIHost {
		if name := strings.TrimSpace(step.Template.Output); name != "" && outputCapture != nil {
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
