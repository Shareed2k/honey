package engine

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"

	"github.com/shareed2k/honey/internal/cuetry"
	"go.uber.org/zap"
)

const defaultGraphStepParallelism = 8

// StreamCueRecipeStepsGraph ...
func StreamCueRecipeStepsGraph(ctx context.Context, run *CueRun, out chan<- HostExecResult) error {
	sg, err := cuetry.BuildStepGraphFromRecipe(&run.Params.Recipe)
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

	tracer := otel.Tracer("honey")
	for wi, wave := range sg.Waves {
		batch := graphWaveBatch(sg, state, wave, &run.Params.Recipe)
		if len(batch) == 0 {
			continue
		}

		err := func() error {
			waveCtx, waveSpan := tracer.Start(ctx, fmt.Sprintf("recipe.wave.%d", wi))
			defer waveSpan.End()
			return runGraphWave(waveCtx, run, out, sg, state, historyByIndex, batch, defaultGraphStepParallelism)
		}()
		if err != nil {
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
				_, _ = StreamCueRecipeStep(ctx, run, -1, handler.Step, nil, out)
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

func graphWaveBatch(sg *cuetry.StepGraph, state []cuetry.StepRunState, wave []int, r *cuetry.Recipe) []int {
	var batch []int
	for _, idx := range wave {
		if state[idx] == cuetry.StepRunSkipped {
			continue
		}
		if graphShouldSkipStep(sg, state, idx, r) {
			state[idx] = cuetry.StepRunSkipped
			continue
		}
		batch = append(batch, idx)
	}
	return batch
}

func graphShouldSkipStep(sg *cuetry.StepGraph, state []cuetry.StepRunState, idx int, r *cuetry.Recipe) bool {
	trigger := "all_success"
	if step := r.Steps[idx].Step; step != nil && step.Base().TriggerRule != "" {
		trigger = step.Base().TriggerRule
	}

	parentFailed := false
	parentSkipped := false

	isRescueTarget := false
	rescueTriggered := false

	for _, d := range sg.Depends[idx] {
		switch state[d] {
		case cuetry.StepRunFailed:
			parentFailed = true
		case cuetry.StepRunSkipped:
			parentSkipped = true
		}

		parentStep := r.Steps[d].Step
		if parentStep != nil {
			for _, res := range parentStep.Base().Rescue {
				if res == sg.IndexToID[idx] {
					isRescueTarget = true
					if state[d] == cuetry.StepRunFailed {
						rescueTriggered = true
					}
				}
			}
		}
	}

	if isRescueTarget {
		// A step designated as a rescue target runs ONLY if the rescued node failed.
		// It bypasses normal trigger rules.
		if rescueTriggered {
			return false // do not skip, run it
		}
		return true // skip it because the node it rescues didn't fail
	}

	switch trigger {
	case "all_success", "":
		return parentFailed || parentSkipped
	case "one_failed":
		// skip if no parent failed
		return !parentFailed
	case "all_done":
		// run regardless of parent outcome, as long as they are done
		// Note: parentSkipped means parent was skipped. If a parent is skipped,
		// should "all_done" run? Typically yes, or maybe not if all are skipped.
		// Let's say all_done skips if all parents skipped.
		if len(sg.Depends[idx]) > 0 {
			allSkipped := true
			for _, d := range sg.Depends[idx] {
				if state[d] != cuetry.StepRunSkipped {
					allSkipped = false
					break
				}
			}
			return allSkipped
		}
		return false
	default:
		return parentFailed || parentSkipped
	}
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

	var rows []HostExecResult
	var stepErr error
	rows, stepErr = StreamCueRecipeStep(ctx, run, idx, step, hist, out)

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
	} else if failed {
		zap.L().Error("Step failed and ignore_errors is false", zap.String("step_id", stepID), zap.Bool("ignore_errors", step.Base().IgnoreErrors))
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
		logGraphStepFinished(stepID, kind, cuetry.StepRunSkipped, nil)
		return
	}
	if failed {
		state[idx] = cuetry.StepRunFailed
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
