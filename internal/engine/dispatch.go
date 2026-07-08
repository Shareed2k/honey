package engine

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

// DispatchHostResults runs fn for each target, bounded by a weighted semaphore
// sized maxConc (falling back to defaultConc when maxConc <= 0, clamped to
// maxConcurrencyCap), and passes each result to sink. If ctx is cancelled
// before a target's turn, fn is not called for that target — sink instead
// receives a synthesized failure result carrying the target's identity and
// the acquire error.
//
// This concentrates the per-target concurrency-dispatch shape shared by every
// step executor that fans work out across hosts; callers keep whatever is
// genuinely specific to them (retries, side-channel error tracking, post
// hooks) inside their own fn/sink closures.
func DispatchHostResults(ctx context.Context, targets []TargetContext, maxConc, defaultConc int, fn func(TargetContext) HostExecResult, sink func(HostExecResult)) {
	if maxConc <= 0 {
		maxConc = defaultConc
	}
	if maxConc > maxConcurrencyCap {
		maxConc = maxConcurrencyCap
	}
	sem := semaphore.NewWeighted(int64(maxConc))
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sem.Acquire(ctx, 1); err != nil {
				sink(HostExecResult{
					Name:     target.Record.Name,
					IP:       target.Record.PrimaryIP,
					Provider: target.Record.Provider,
					Success:  false,
					ErrMsg:   err.Error(),
				})
				return
			}
			defer sem.Release(1)
			sink(fn(target))
		}()
	}
	wg.Wait()
}
