package engine

import (
	"context"
	"sync/atomic"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// stepExecFn streams one step's execution against its targets. It replaces the old
// DispatchStepByKind switch: each kind registers its streaming function here, and
// DispatchStepByKind is a registry lookup. (Execution must live in this package —
// internal/cuetry cannot import internal/ui, so a Step.Execute() method is impossible.)
type stepExecFn func(ctx context.Context, run *CueRun, i int, step cuetry.Step, targets []hosts.Record, ch chan<- HostExecResult, retryCfg cuetry.RecipeStepRetry, attemptMax *atomic.Int32) error

var stepExecRegistry = map[string]stepExecFn{}

func registerStepExec(kind string, fn stepExecFn) { stepExecRegistry[kind] = fn }

func init() {
	registerStepExec(cuetry.KindCommand, func(ctx context.Context, run *CueRun, i int, step cuetry.Step, targets []hosts.Record, ch chan<- HostExecResult, rc cuetry.RecipeStepRetry, am *atomic.Int32) error {
		return StreamCueStepCommand(ctx, run, i, step.Kind(), step, targets, ch, rc, am)
	})
	registerStepExec(cuetry.KindScript, func(ctx context.Context, run *CueRun, i int, step cuetry.Step, targets []hosts.Record, ch chan<- HostExecResult, rc cuetry.RecipeStepRetry, am *atomic.Int32) error {
		return StreamCueStepScript(ctx, run, i, step.Kind(), step, targets, ch, rc, am)
	})
	registerStepExec(cuetry.KindPlugin, func(ctx context.Context, run *CueRun, i int, step cuetry.Step, targets []hosts.Record, ch chan<- HostExecResult, rc cuetry.RecipeStepRetry, am *atomic.Int32) error {
		return StreamCueStepPlugin(ctx, run, i, step.Kind(), step, targets, ch, rc, am)
	})
	registerStepExec(cuetry.KindPut, func(ctx context.Context, run *CueRun, _ int, step cuetry.Step, targets []hosts.Record, ch chan<- HostExecResult, rc cuetry.RecipeStepRetry, am *atomic.Int32) error {
		return StreamCueStepPut(ctx, run, step, targets, ch, rc, am)
	})
	registerStepExec(cuetry.KindGet, func(ctx context.Context, run *CueRun, _ int, step cuetry.Step, targets []hosts.Record, ch chan<- HostExecResult, rc cuetry.RecipeStepRetry, am *atomic.Int32) error {
		return StreamCueStepGet(ctx, run, step, targets, ch, rc, am)
	})
	// These streaming functions already match stepExecFn exactly.
	registerStepExec(cuetry.KindTunnel, StreamCueStepTunnel)
	registerStepExec(cuetry.KindK8s, StreamCueStepK8s)
	registerStepExec(cuetry.KindDocker, StreamCueStepDocker)
	registerStepExec(cuetry.KindOpensearch, StreamCueStepOpensearch)
	registerStepExec(cuetry.KindPostgres, StreamCueStepPostgres)
}
