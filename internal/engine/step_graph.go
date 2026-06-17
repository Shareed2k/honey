package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/shareed2k/honey/internal/cuetry"
	"go.uber.org/zap"
)

const defaultGraphStepParallelism = 8

// StreamCueRecipeStepsGraph ...
func StreamCueRecipeStepsGraph(ctx context.Context, run *CueRun, out chan<- HostExecResult) error {
	sg, err := cuetry.BuildStepGraphFromRecipe(run.Params.Recipe)
	if err != nil {
		return err
	}
	if err := EnsureKVSessionForRecipe(run.Params.Recipe, run.RecipeKV, run.Params.Execute); err != nil {
		return err
	}
	run.OutputStore = cuetry.NewStepOutputStore()
	run.OutputCapture = cuetry.NewRecipeOutputCapture()
	n := len(run.Params.Recipe.Steps)
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
		if err := runGraphWave(ctx, run, out, sg, state, historyByIndex, batch, defaultGraphStepParallelism); err != nil {
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

	// Run triggered handlers
	if len(run.TriggeredHandlers) > 0 && len(run.Params.Recipe.Handlers) > 0 {
		zap.L().Debug("executing triggered handlers", zap.Any("triggered", run.TriggeredHandlers))
		for _, handler := range run.Params.Recipe.Handlers {
			hid := handler.Step.Base().ID
			if run.TriggeredHandlers[hid] {
				zap.L().Info("running handler", zap.String("id", hid))
				_, _ = StreamCueRecipeStep(ctx, run, -1, handler.Step, out)
			}
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

func runGraphWave(ctx context.Context, run *CueRun, out chan<- HostExecResult, sg *cuetry.StepGraph, state []cuetry.StepRunState, historyByIndex [][]HostExecResult, batch []int, parallelism int) error {
	stepIDs := make([]string, len(batch))
	for i, idx := range batch {
		stepIDs[i] = sg.IndexToID[idx]
	}
	zap.L().Debug("recipe graph wave", zap.Strings("step_ids", stepIDs))

	var wg sync.WaitGroup
	sem := make(chan struct{}, parallelism)
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
			graphRunOneStep(ctx, run, out, sg, state, historyByIndex, &stateMu, &historyMu, idx)
		}(idx)
	}
	wg.Wait()
	return graphAbortIfAIUnreachable(sg, state)
}

func graphRunOneStep(ctx context.Context, run *CueRun, out chan<- HostExecResult, sg *cuetry.StepGraph, state []cuetry.StepRunState, historyByIndex [][]HostExecResult, stateMu *sync.Mutex, historyMu *sync.Mutex, idx int) {
	step := run.Params.Recipe.Steps[idx].Step
	stepID := sg.IndexToID[idx]
	kind := step.Kind()

	var rows []HostExecResult
	var stepErr error
	switch kind {
	case cuetry.KindAI:
		rows, stepErr = graphRunAIStep(ctx, run, idx, step, sg, state, historyByIndex, stateMu, historyMu, out)
	case cuetry.KindTemplate:
		rows, stepErr = graphRunTemplateStep(ctx, run, idx, step, out)
	default:
		rows, stepErr = StreamCueRecipeStep(ctx, run, idx, step, out)
	}

	failed := stepErr != nil
	if !failed && len(rows) > 0 {
		for _, r := range rows {
			if !r.Skipped && !r.Success {
				failed = true
				break
			}
		}
	}

	if failed && step.Base().IgnoreErrors {
		zap.L().Warn(
			"Step failed but ignore_errors is true. Marking as succeeded to let descendants run.",
			zap.String("step_id", stepID),
		)
		failed = false
	}

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
		logGraphStepFinished(stepID, kind, cuetry.StepRunSkipped, nil)
		return
	}
	if failed {
		state[idx] = cuetry.StepRunFailed
		sg.MarkSkippedDescendants(idx, state)
		logGraphStepFinished(stepID, kind, cuetry.StepRunFailed, stepErr)
		return
	}
	state[idx] = cuetry.StepRunSucceeded
	logGraphStepFinished(stepID, kind, cuetry.StepRunSucceeded, nil)
	if step.Base().NotifyEnabled() && len(rows) > 0 {
		body := FormatCueStepHostResultsForNotify(idx+1, rows)
		CueStepNotifyRemote(ctx, run.Params.Recipe, idx+1, kind, step.Base().Notify, body)
	}
}

func graphRunAIStep(ctx context.Context, run *CueRun, idx int, step cuetry.Step, sg *cuetry.StepGraph, state []cuetry.StepRunState, historyByIndex [][]HostExecResult, stateMu *sync.Mutex, historyMu *sync.Mutex, out chan<- HostExecResult) ([]HostExecResult, error) {
	kv := KvReaderFromCoordinator(run.RecipeKV)
	ok, whenErr := EvalAIStepWhen(ctx, run.Params.Recipe, step, run.OutputStore, run.Params.SecretResolver, kv, run.Params.CLIEnv, run.Params.Execute)
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
		AnnotateCueStepResult(&res, idx, step, cuetry.KindAI)
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
	res := RunCueStepAIExecute(ctx, run.Params.Recipe, idx, step, hist, run.Params.AISystemPrompt)
	AnnotateCueStepResult(&res, idx, step, cuetry.KindAI)
	out <- res
	if !res.Success {
		return []HostExecResult{res}, fmt.Errorf("ai step failed: %s", res.ErrMsg)
	}
	return []HostExecResult{res}, nil
}

func graphRunTemplateStep(ctx context.Context, run *CueRun, idx int, step cuetry.Step, out chan<- HostExecResult) ([]HostExecResult, error) {
	rows, err := run.streamCueTemplateStep(ctx, idx, step, out)
	if err != nil {
		return rows, err
	}
	return rows, nil
}

func logGraphStepFinished(stepID string, kind string, st cuetry.StepRunState, err error) {
	fields := []zap.Field{
		zap.String("step_id", stepID),
		zap.String("kind", kind),
		zap.String("state", graphStepRunStateLabel(st)),
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
	}
	zap.L().Debug("recipe graph step finished", fields...)
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
