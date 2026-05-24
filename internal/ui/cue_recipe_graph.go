package ui

import (
	"context"
	"fmt"
	"sync"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/plugins"
	"go.uber.org/zap"
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
	obs metrics.Observer,
	out chan<- HostExecResult,
	cache *ClientCache,
	recipeKV *RecipeKVCoordinator,
	tunnelCoord *RecipeTunnelCoordinator,
) error {
	sg, err := cuetry.BuildStepGraphFromRecipe(recipe)
	if err != nil {
		return err
	}
	if err := ensureKVSessionForRecipe(recipe, recipeKV, execute); err != nil {
		return err
	}
	outputStore := cuetry.NewStepOutputStore()
	outputCapture := cuetry.NewRecipeOutputCapture()
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
		if err := runGraphWave(ctx, recipe, recipeDir, records, sshUser, cliEnv, configPath, aiSystemPromptFromCfg, secretResolver, pluginMgr, execute, obs, out, cache, recipeKV, tunnelCoord, outputStore, outputCapture, sg, state, historyByIndex, batch); err != nil {
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
	obs metrics.Observer,
	out chan<- HostExecResult,
	cache *ClientCache,
	recipeKV *RecipeKVCoordinator,
	tunnelCoord *RecipeTunnelCoordinator,
	outputStore *cuetry.StepOutputStore,
	outputCapture *cuetry.RecipeOutputCapture,
	sg *cuetry.StepGraph,
	state []cuetry.StepRunState,
	historyByIndex [][]HostExecResult,
	batch []int,
) error {
	stepIDs := make([]string, len(batch))
	for i, idx := range batch {
		stepIDs[i] = sg.IndexToID[idx]
	}
	zap.L().Debug("recipe graph wave", zap.Strings("step_ids", stepIDs))

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
			graphRunOneStep(ctx, recipe, recipeDir, records, sshUser, cliEnv, configPath, aiSystemPromptFromCfg, secretResolver, pluginMgr, execute, obs, out, cache, recipeKV, tunnelCoord, outputStore, outputCapture, sg, state, historyByIndex, &stateMu, &historyMu, idx)
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
	obs metrics.Observer,
	out chan<- HostExecResult,
	cache *ClientCache,
	recipeKV *RecipeKVCoordinator,
	tunnelCoord *RecipeTunnelCoordinator,
	outputStore *cuetry.StepOutputStore,
	outputCapture *cuetry.RecipeOutputCapture,
	sg *cuetry.StepGraph,
	state []cuetry.StepRunState,
	historyByIndex [][]HostExecResult,
	stateMu *sync.Mutex,
	historyMu *sync.Mutex,
	idx int,
) {
	step := recipe.Steps[idx]
	stepID := sg.IndexToID[idx]
	kind, classifyErr := cuetry.ClassifyStep(step)
	if classifyErr != nil {
		zap.L().Debug("recipe graph step finished",
			zap.String("step_id", stepID),
			zap.String("kind", "unknown"),
			zap.String("state", "failed"),
		)
		stateMu.Lock()
		state[idx] = cuetry.StepRunFailed
		sg.MarkSkippedDescendants(idx, state)
		stateMu.Unlock()
		return
	}

	var rows []HostExecResult
	var stepErr error
	switch kind {
	case cuetry.StepKindAI:
		rows, stepErr = graphRunAIStep(ctx, recipe, idx, step, sg, state, historyByIndex, stateMu, historyMu, aiSystemPromptFromCfg, outputStore, secretResolver, recipeKV, cliEnv, execute, out)
	case cuetry.StepKindTemplate:
		rows, stepErr = graphRunTemplateStep(ctx, recipe, recipeDir, idx, step, records, outputStore, outputCapture, secretResolver, recipeKV, execute, out)
	default:
		rows, stepErr = streamCueRecipeStep(ctx, recipe, recipeDir, records, sshUser, cliEnv, configPath, idx, step, out, cache, recipeKV, tunnelCoord, outputStore, outputCapture, secretResolver, pluginMgr, execute, obs)
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
		logGraphStepFinished(stepID, kind, cuetry.StepRunSkipped)
		return
	}
	if failed {
		state[idx] = cuetry.StepRunFailed
		sg.MarkSkippedDescendants(idx, state)
		logGraphStepFinished(stepID, kind, cuetry.StepRunFailed)
		return
	}
	state[idx] = cuetry.StepRunSucceeded
	logGraphStepFinished(stepID, kind, cuetry.StepRunSucceeded)
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
	cliEnv map[string]string,
	execute bool,
	out chan<- HostExecResult,
) ([]HostExecResult, error) {
	kv := kvReaderFromCoordinator(recipeKV)
	ok, whenErr := evalAIStepWhen(ctx, recipe, step, outputStore, secretResolver, kv, cliEnv, execute)
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

func graphRunTemplateStep(
	ctx context.Context,
	recipe cuetry.Recipe,
	recipeDir string,
	idx int,
	step cuetry.RecipeStep,
	records []hosts.Record,
	outputStore *cuetry.StepResultStore,
	outputCapture *cuetry.RecipeOutputCapture,
	secretResolver cuetry.SecretResolver,
	recipeKV *RecipeKVCoordinator,
	execute bool,
	out chan<- HostExecResult,
) ([]HostExecResult, error) {
	rows, err := streamCueTemplateStep(ctx, recipe, recipeDir, idx, step, records, outputStore, outputCapture, recipeKV, secretResolver, execute, out)
	if err != nil {
		return rows, err
	}
	return rows, nil
}

func logGraphStepFinished(stepID string, kind cuetry.StepKind, st cuetry.StepRunState) {
	zap.L().Debug("recipe graph step finished",
		zap.String("step_id", stepID),
		zap.String("kind", cuetry.StepKindLabel(kind)),
		zap.String("state", graphStepRunStateLabel(st)),
	)
}

func graphStepRunStateLabel(st cuetry.StepRunState) string {
	switch st {
	case cuetry.StepRunSucceeded:
		return "succeeded"
	case cuetry.StepRunFailed:
		return "failed"
	case cuetry.StepRunSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}
