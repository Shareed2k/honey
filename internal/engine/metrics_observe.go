package engine

import (
	"math"
	"sync/atomic"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/metrics"
)

func recipeTypeLabel(recipe cuetry.Recipe) string {
	mode, err := cuetry.RecipeExecutionMode(recipe)
	if err != nil {
		return "linear"
	}
	if mode == cuetry.ExecutionModeGraph {
		return "graph"
	}
	return "linear"
}

// ObserveRecipeRun ...
func ObserveRecipeRun(obs metrics.Observer, recipe cuetry.Recipe, execute bool, start time.Time, err error) {
	if !metrics.ObserverEnabled(obs) {
		return
	}
	mode := "dry_run"
	if execute {
		mode = "execute"
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	obs.ObserveRecipeRun(mode, recipeTypeLabel(recipe), status, time.Since(start))
}

func hostResultStatus(res HostExecResult) string {
	if res.Skipped {
		return "skipped"
	}
	if res.Success {
		return "ok"
	}
	return "error"
}

func observeRecipeHostResult(obs metrics.Observer, res HostExecResult) {
	if !metrics.ObserverEnabled(obs) {
		return
	}
	obs.ObserveRecipeHostResult(hostResultStatus(res))
}

// ObserveRecipeStep ...
func ObserveRecipeStep(obs metrics.Observer, kind string, start time.Time, rows []HostExecResult, retryAttempts int) {
	if !metrics.ObserverEnabled(obs) {
		return
	}
	kindLabel := kind
	status := classifyStepStatus(rows)
	obs.ObserveRecipeStep(kindLabel, status, time.Since(start), retryAttempts)
	for _, row := range rows {
		observeRecipeHostResult(obs, row)
	}
}

func classifyStepStatus(rows []HostExecResult) string {
	if len(rows) == 0 {
		return "ok"
	}
	allSkipped := true
	anyError := false
	for _, row := range rows {
		if !row.Skipped {
			allSkipped = false
		}
		if !row.Skipped && !row.Success {
			anyError = true
		}
	}
	if allSkipped {
		return "skipped"
	}
	if anyError {
		return "error"
	}
	return "ok"
}

// PluginExecStatus ...
func PluginExecStatus(success, skipped bool) string {
	if skipped {
		return "skipped"
	}
	if success {
		return "ok"
	}
	return "error"
}

// RecordMaxAttempts ...
func RecordMaxAttempts(attemptMax *atomic.Int32, attempts int) {
	if attemptMax == nil || attempts <= 0 {
		return
	}
	if attempts > math.MaxInt32 {
		attempts = math.MaxInt32
	}
	next := int32(attempts)
	for {
		cur := attemptMax.Load()
		if next <= cur {
			return
		}
		if attemptMax.CompareAndSwap(cur, next) {
			return
		}
	}
}

func observeSSHOperation(obs metrics.Observer, op, status string, d time.Duration) {
	if !metrics.ObserverEnabled(obs) {
		return
	}
	obs.ObserveSSHOperation(op, status, d)
}
