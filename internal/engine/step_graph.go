package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/shareed2k/honey/internal/cuetry"
	"go.uber.org/zap"
)

const defaultGraphStepParallelism = 8

// graphStepParallelism resolves how many graph steps may run concurrently: the
// dataflow scheduler's worker-pool size. Uses recipe defaults.max_parallel
// (which itself carries the config-level default, seeded before dispatch) when
// set, else the built-in 8. Clamped 1-128.
func graphStepParallelism(recipe *cuetry.Recipe) int {
	if recipe != nil && recipe.Defaults != nil && recipe.Defaults.MaxParallel > 0 {
		n := recipe.Defaults.MaxParallel
		if n > 128 {
			n = 128
		}
		return n
	}
	return defaultGraphStepParallelism
}

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

	// stateMu is the single owner of state[]; it is shared between the scheduler
	// (seeding/skip/cascade) and each step run (graphRunOneStep reads the
	// succeeded-set and writes the step's terminal state under it). historyMu is
	// separate because graphRunOneStep nests it under stateMu.
	var stateMu sync.Mutex
	var historyMu sync.Mutex
	runStep := func(sctx context.Context, idx int) {
		graphRunOneStep(sctx, run, out, sg, state, historyByIndex, &stateMu, &historyMu, idx)
	}
	runGraphDataflow(ctx, sg, state, &run.Params.Recipe, graphStepParallelism(&run.Params.Recipe), &stateMu, runStep)

	if err := graphAbortIfSummarizeUnreachable(sg, state); err != nil {
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

func graphAbortIfSummarizeUnreachable(sg *cuetry.StepGraph, state []cuetry.StepRunState) error {
	if sg.SummarizeIndex < 0 {
		return nil
	}
	if state[sg.SummarizeIndex] == cuetry.StepRunSkipped {
		return fmt.Errorf("summarize step %q unreachable: a dependency failed or was skipped", sg.IndexToID[sg.SummarizeIndex])
	}
	return nil
}

// runGraphDataflow schedules graph steps by true dataflow: each step becomes
// runnable the instant ALL of its dependencies reach a terminal state
// (Succeeded/Failed/Skipped), rather than waiting for the slowest peer in its
// dependency "wave". A worker pool of `parallelism` goroutines drains a
// ready-channel; a completed step cascades to its dependents, and a step whose
// trigger rule is unmet is marked skipped (which itself cascades). This removes
// the barrier between dependency levels while preserving the skip/rescue/failure
// semantics of the former wave scheduler.
//
// Concurrency contract: stateMu is the single owner of state[]; the scheduler
// uses it to guard state, remaining, pending, readyStack and closed, and it is
// the SAME mutex runStep locks internally to read the succeeded-set and write a
// step's terminal state. runStep is invoked WITHOUT holding stateMu (it locks
// internally) and MUST set state[idx] to a terminal value before returning. The
// ready channel is buffered to n and each step is enqueued at most once, so
// sends never block — safe to send while holding stateMu.
func runGraphDataflow(ctx context.Context, sg *cuetry.StepGraph, state []cuetry.StepRunState, r *cuetry.Recipe, parallelism int, stateMu *sync.Mutex, runStep func(context.Context, int)) {
	n := len(sg.IndexToID)
	if n == 0 {
		return
	}
	if parallelism < 1 {
		parallelism = 1
	}

	// Reverse edges + outstanding-dependency counts.
	dependents := make([][]int, n)
	remaining := make([]int, n)
	for i := 0; i < n; i++ {
		remaining[i] = len(sg.Depends[i])
		for _, d := range sg.Depends[i] {
			dependents[d] = append(dependents[d], i)
		}
	}

	var (
		pending    = n
		closed     bool
		readyStack []int
	)
	ready := make(chan int, n)
	done := make(chan struct{})

	closeDone := func() {
		if !closed {
			closed = true
			close(done)
		}
	}

	// terminal records that step idx reached a terminal state (called under
	// stateMu): drop the pending count and unblock any dependent whose final
	// dependency just finished.
	terminal := func(idx int) {
		pending--
		if pending == 0 {
			closeDone()
		}
		for _, dep := range dependents[idx] {
			remaining[dep]--
			if remaining[dep] == 0 {
				readyStack = append(readyStack, dep)
			}
		}
	}

	// drain resolves every scheduled step (deps all terminal) to a skip
	// (mark + cascade) or a run (enqueue). Iterative so a skip-cascade never
	// recurses while holding the lock. Called under stateMu.
	drain := func() {
		for len(readyStack) > 0 {
			idx := readyStack[len(readyStack)-1]
			readyStack = readyStack[:len(readyStack)-1]
			if graphShouldSkipStep(sg, state, idx, r) {
				state[idx] = cuetry.StepRunSkipped
				if idx < len(r.Steps) {
					if step := r.Steps[idx].Step; step != nil {
						logGraphStepFinished(sg.IndexToID[idx], step.Kind(), cuetry.StepRunSkipped, nil)
					}
				}
				terminal(idx)
				continue
			}
			ready <- idx // buffered (cap n), never blocks
		}
	}

	// Seed roots (no dependencies), resolving any immediate skip-cascade.
	stateMu.Lock()
	for i := 0; i < n; i++ {
		if remaining[i] == 0 {
			readyStack = append(readyStack, i)
		}
	}
	drain()
	stateMu.Unlock()

	var wg sync.WaitGroup
	for w := 0; w < parallelism; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case idx := <-ready:
					// Don't start new steps once cancelled (matches the former
					// wave scheduler, which returned before running on ctx.Done).
					if ctx.Err() != nil {
						return
					}
					runStep(ctx, idx)
					stateMu.Lock()
					terminal(idx)
					drain()
					stateMu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
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
