package ui

import (
	"context"
	"fmt"
	"sync"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
)

const defaultGraphStepParallelism = 8

func streamCueRecipeStepsGraph(
	ctx context.Context,
	recipe cuetry.Recipe,
	recipeDir string,
	records []hosts.Record,
	sshUser string,
	cliEnv map[string]string,
	configPath string,
	aiSystemPromptFromCfg string,
	secretResolver cuetry.SecretResolver,
	pluginMgr *plugins.Manager,
	execute bool,
	out chan<- HostExecResult,
	cache *ClientCache,
	recipeKV *RecipeKVCoordinator,
) error {
	sg, err := cuetry.BuildStepGraphFromRecipe(recipe)
	if err != nil {
		return err
	}
	if cuetry.RecipeHasKVTunnel(recipe) {
		if _, err := recipeKV.EnsureSession(); err != nil {
			return err
		}
	}
	if err := ensureKVSessionForWhen(recipe, recipeKV); err != nil {
		return err
	}
	outputStore := cuetry.NewStepOutputStore()
	n := len(recipe.Steps)
	state := make([]cuetry.StepRunState, n)
	historyByIndex := make([][]HostExecResult, n)

	for i := range state {
		state[i] = cuetry.StepRunPending
	}

	for _, wave := range sg.Waves {
		batch := graphWaveBatch(sg, state, wave)
		if len(batch) == 0 {
			continue
		}
		if err := runGraphWave(ctx, recipe, recipeDir, records, sshUser, cliEnv, configPath, aiSystemPromptFromCfg, secretResolver, pluginMgr, execute, out, cache, recipeKV, outputStore, sg, state, historyByIndex, batch); err != nil {
			return err
		}
	}
	if err := graphAbortIfAIUnreachable(sg, state); err != nil {
		return err
	}
	for i, st := range state {
		if st == cuetry.StepRunFailed {
			return fmt.Errorf("recipe graph finished with failed step %q", sg.IndexToID[i])
		}
	}
	return nil
}

func graphAbortIfAIUnreachable(sg *cuetry.StepGraph, state []cuetry.StepRunState) error {
	if sg.AIIndex < 0 {
		return nil
	}
	if state[sg.AIIndex] == cuetry.StepRunSkipped {
		return fmt.Errorf("ai step %q unreachable: a dependency failed or was skipped", sg.IndexToID[sg.AIIndex])
	}
	return nil
}

func graphWaveBatch(sg *cuetry.StepGraph, state []cuetry.StepRunState, wave []int) []int {
	var batch []int
	for _, idx := range wave {
		if state[idx] == cuetry.StepRunSkipped {
			continue
		}
		if graphShouldSkipStep(sg, state, idx) {
			state[idx] = cuetry.StepRunSkipped
			sg.MarkSkippedDescendants(idx, state)
			continue
		}
		batch = append(batch, idx)
	}
	return batch
}

func graphShouldSkipStep(sg *cuetry.StepGraph, state []cuetry.StepRunState, idx int) bool {
	for _, d := range sg.Depends[idx] {
		switch state[d] {
		case cuetry.StepRunFailed, cuetry.StepRunSkipped:
			return true
		case cuetry.StepRunSucceeded:
		default:
			return true
		}
	}
	return false
}

func runGraphWave(
	ctx context.Context,
	recipe cuetry.Recipe,
	recipeDir string,
	records []hosts.Record,
	sshUser string,
	cliEnv map[string]string,
	configPath string,
	aiSystemPromptFromCfg string,
	secretResolver cuetry.SecretResolver,
	pluginMgr *plugins.Manager,
	execute bool,
	out chan<- HostExecResult,
	cache *ClientCache,
	recipeKV *RecipeKVCoordinator,
	outputStore *cuetry.StepOutputStore,
	sg *cuetry.StepGraph,
	state []cuetry.StepRunState,
	historyByIndex [][]HostExecResult,
	batch []int,
) error {
	var wg sync.WaitGroup
	sem := make(chan struct{}, defaultGraphStepParallelism)
	var stateMu sync.Mutex
	var historyMu sync.Mutex

	for _, idx := range batch {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			graphRunOneStep(ctx, recipe, recipeDir, records, sshUser, cliEnv, configPath, aiSystemPromptFromCfg, secretResolver, pluginMgr, execute, out, cache, recipeKV, outputStore, sg, state, historyByIndex, &stateMu, &historyMu, idx)
		}(idx)
	}
	wg.Wait()
	return graphAbortIfAIUnreachable(sg, state)
}

func graphRunOneStep(
	ctx context.Context,
	recipe cuetry.Recipe,
	recipeDir string,
	records []hosts.Record,
	sshUser string,
	cliEnv map[string]string,
	configPath string,
	aiSystemPromptFromCfg string,
	secretResolver cuetry.SecretResolver,
	pluginMgr *plugins.Manager,
	execute bool,
	out chan<- HostExecResult,
	cache *ClientCache,
	recipeKV *RecipeKVCoordinator,
	outputStore *cuetry.StepOutputStore,
	sg *cuetry.StepGraph,
	state []cuetry.StepRunState,
	historyByIndex [][]HostExecResult,
	stateMu *sync.Mutex,
	historyMu *sync.Mutex,
	idx int,
) {
	step := recipe.Steps[idx]
	kind, classifyErr := cuetry.ClassifyStep(step)
	if classifyErr != nil {
		stateMu.Lock()
		state[idx] = cuetry.StepRunFailed
		sg.MarkSkippedDescendants(idx, state)
		stateMu.Unlock()
		return
	}

	var rows []HostExecResult
	var stepErr error
	if kind == cuetry.StepKindAI {
		rows, stepErr = graphRunAIStep(ctx, recipe, idx, step, sg, state, historyByIndex, stateMu, historyMu, aiSystemPromptFromCfg, outputStore, secretResolver, recipeKV, execute, out)
	} else {
		rows, stepErr = streamCueRecipeStep(ctx, recipe, recipeDir, records, sshUser, cliEnv, configPath, idx, step, out, cache, recipeKV, outputStore, secretResolver, pluginMgr, execute)
	}

	failed := stepErr != nil || (len(rows) > 0 && cueStepAllTargetsTransientTransportFailed(rows))
	stateMu.Lock()
	defer stateMu.Unlock()
	if len(rows) > 0 {
		historyMu.Lock()
		historyByIndex[idx] = rows
		historyMu.Unlock()
	}
	if len(rows) > 0 && allHostsWhenSkipped(rows) {
		state[idx] = cuetry.StepRunSkipped
		sg.MarkSkippedDescendants(idx, state)
		return
	}
	if failed {
		state[idx] = cuetry.StepRunFailed
		sg.MarkSkippedDescendants(idx, state)
		return
	}
	state[idx] = cuetry.StepRunSucceeded
	if step.NotifyEnabled() && len(rows) > 0 {
		body := FormatCueStepHostResultsForNotify(idx+1, rows)
		CueStepNotifyRemote(ctx, recipe, idx+1, kind, step.Notify, body)
	}
}

func graphRunAIStep(
	ctx context.Context,
	recipe cuetry.Recipe,
	idx int,
	step cuetry.RecipeStep,
	sg *cuetry.StepGraph,
	state []cuetry.StepRunState,
	historyByIndex [][]HostExecResult,
	stateMu *sync.Mutex,
	historyMu *sync.Mutex,
	aiSystemPromptFromCfg string,
	outputStore *cuetry.StepResultStore,
	secretResolver cuetry.SecretResolver,
	recipeKV *RecipeKVCoordinator,
	execute bool,
	out chan<- HostExecResult,
) ([]HostExecResult, error) {
	kv := kvReaderFromCoordinator(recipeKV)
	ok, whenErr := evalAIStepWhen(ctx, recipe, step, outputStore, secretResolver, kv, execute)
	if whenErr != nil {
		return nil, whenErr
	}
	if !ok {
		res := HostExecResult{
			Name:     fmt.Sprintf("Step %d | ai", idx+1),
			Provider: "local",
			Skipped:  true,
			Output:   "(skipped: when)",
		}
		out <- res
		return []HostExecResult{res}, nil
	}
	stateMu.Lock()
	succeeded := make(map[int]bool, len(state))
	for j, st := range state {
		if st == cuetry.StepRunSucceeded {
			succeeded[j] = true
		}
	}
	stateMu.Unlock()
	historyMu.Lock()
	var hist [][]HostExecResult
	for _, j := range sg.AncestorHistoryOrder(idx, succeeded) {
		if h := historyByIndex[j]; len(h) > 0 {
			hist = append(hist, h)
		}
	}
	historyMu.Unlock()
	res := runCueStepAIExecute(ctx, recipe, idx, step, hist, aiSystemPromptFromCfg)
	out <- res
	if !res.Success {
		return []HostExecResult{res}, fmt.Errorf("ai step failed: %s", res.ErrMsg)
	}
	return []HostExecResult{res}, nil
}
