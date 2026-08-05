package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
)

// --- test harness -----------------------------------------------------------

type stepSpec struct {
	id      string
	depends []string
	trigger string
	rescue  []string
}

// buildGraph builds a validated graph StepGraph + Recipe from lightweight specs,
// so scheduler tests exercise the real BuildStepGraph deps/rescue/topo logic
// without any host execution.
func buildGraph(tb testing.TB, specs ...stepSpec) (*cuetry.StepGraph, *cuetry.Recipe) {
	tb.Helper()
	steps := make([]cuetry.Step, len(specs))
	for i, s := range specs {
		steps[i] = &cuetry.CommandStep{
			StepBase: cuetry.StepBase{
				ID:          s.id,
				Depends:     s.depends,
				TriggerRule: s.trigger,
				Rescue:      s.rescue,
				Host:        "*",
			},
			Command: "true",
		}
	}
	r := &cuetry.Recipe{Type: "graph", Name: "t", Steps: wrapSteps(steps...)}
	sg, err := cuetry.BuildStepGraphFromRecipe(r)
	if err != nil {
		tb.Fatalf("build graph: %v", err)
	}
	return sg, r
}

// --- correctness tests ------------------------------------------------------

// TestDataflow_IndependentStepDoesNotWaitOnSlowPeer is the core property of the
// dataflow scheduler: "fc" (depth 2, via fast) becomes runnable the instant its
// own dependency "fast" completes, without waiting for its slow level peer.
// We block "slow" until fc has completed — the former wave scheduler would
// deadlock here (fc's wave can't start until slow's wave finishes), the
// dataflow scheduler completes fc first.
func TestDataflow_IndependentStepDoesNotWaitOnSlowPeer(t *testing.T) {
	sg, r := buildGraph(t,
		stepSpec{id: "a"},
		stepSpec{id: "fast", depends: []string{"a"}},
		stepSpec{id: "slow", depends: []string{"a"}},
		stepSpec{id: "fc", depends: []string{"fast"}},
	)
	idx := sg.IDToIndex
	state := make([]cuetry.StepRunState, len(sg.IndexToID))
	var stateMu sync.Mutex

	fcDone := make(chan struct{})
	slowRelease := make(chan struct{})
	runStep := func(_ context.Context, i int) {
		switch i {
		case idx["slow"]:
			<-slowRelease
		case idx["fc"]:
			close(fcDone)
		}
		stateMu.Lock()
		state[i] = cuetry.StepRunSucceeded
		stateMu.Unlock()
	}

	schedDone := make(chan struct{})
	go func() {
		runGraphDataflow(context.Background(), sg, state, r, 4, &stateMu, runStep)
		close(schedDone)
	}()

	select {
	case <-fcDone:
		// fc finished while slow is still blocked → dataflow property holds.
	case <-time.After(3 * time.Second):
		close(slowRelease)
		t.Fatal("fc did not complete while its slow peer was blocked (wave-style barrier?)")
	}
	close(slowRelease)

	select {
	case <-schedDone:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not finish")
	}
	for i, id := range sg.IndexToID {
		if state[i] != cuetry.StepRunSucceeded {
			t.Errorf("step %q = %v, want Succeeded", id, state[i])
		}
	}
}

// TestDataflow_FailedDepSkipsDependentChain: a failed step skips its dependents
// (default all_success), and the skip cascades down the chain — skipped steps
// never run.
func TestDataflow_FailedDepSkipsDependentChain(t *testing.T) {
	sg, r := buildGraph(t,
		stepSpec{id: "a"},
		stepSpec{id: "b", depends: []string{"a"}},
		stepSpec{id: "c", depends: []string{"b"}},
	)
	idx := sg.IDToIndex
	state := make([]cuetry.StepRunState, len(sg.IndexToID))
	var stateMu sync.Mutex
	var ran sync.Map
	runStep := func(_ context.Context, i int) {
		ran.Store(i, true)
		st := cuetry.StepRunSucceeded
		if i == idx["a"] {
			st = cuetry.StepRunFailed
		}
		stateMu.Lock()
		state[i] = st
		stateMu.Unlock()
	}
	runGraphDataflow(context.Background(), sg, state, r, 4, &stateMu, runStep)

	if state[idx["a"]] != cuetry.StepRunFailed {
		t.Errorf("a = %v, want Failed", state[idx["a"]])
	}
	if state[idx["b"]] != cuetry.StepRunSkipped {
		t.Errorf("b = %v, want Skipped", state[idx["b"]])
	}
	if state[idx["c"]] != cuetry.StepRunSkipped {
		t.Errorf("c = %v, want Skipped", state[idx["c"]])
	}
	if _, ok := ran.Load(idx["b"]); ok {
		t.Error("b should not have run")
	}
	if _, ok := ran.Load(idx["c"]); ok {
		t.Error("c should not have run")
	}
}

// TestDataflow_FanInWaitsForAllDeps: a fan-in step runs exactly once and only
// after ALL of its dependencies are terminal.
func TestDataflow_FanInWaitsForAllDeps(t *testing.T) {
	sg, r := buildGraph(t,
		stepSpec{id: "a"},
		stepSpec{id: "b"},
		stepSpec{id: "c"},
		stepSpec{id: "d", depends: []string{"a", "b", "c"}},
	)
	idx := sg.IDToIndex
	state := make([]cuetry.StepRunState, len(sg.IndexToID))
	var stateMu sync.Mutex
	var dRuns atomic.Int64
	var depsAtD [3]cuetry.StepRunState
	runStep := func(_ context.Context, i int) {
		if i == idx["d"] {
			dRuns.Add(1)
			stateMu.Lock()
			depsAtD[0] = state[idx["a"]]
			depsAtD[1] = state[idx["b"]]
			depsAtD[2] = state[idx["c"]]
			stateMu.Unlock()
		}
		stateMu.Lock()
		state[i] = cuetry.StepRunSucceeded
		stateMu.Unlock()
	}
	runGraphDataflow(context.Background(), sg, state, r, 4, &stateMu, runStep)

	if got := dRuns.Load(); got != 1 {
		t.Fatalf("fan-in step d ran %d times, want 1", got)
	}
	for k, st := range depsAtD {
		if st != cuetry.StepRunSucceeded {
			t.Errorf("dependency %d state observed at d start = %v, want Succeeded", k, st)
		}
	}
}

// TestDataflow_DiamondEachRunsOnce: a diamond DAG runs each step exactly once
// and respects dependency ordering, at both parallelism 1 and 4.
func TestDataflow_DiamondEachRunsOnce(t *testing.T) {
	for _, parallelism := range []int{1, 4} {
		parallelism := parallelism
		t.Run("parallelism="+itoa(parallelism), func(t *testing.T) {
			sg, r := buildGraph(t,
				stepSpec{id: "a"},
				stepSpec{id: "b", depends: []string{"a"}},
				stepSpec{id: "c", depends: []string{"a"}},
				stepSpec{id: "d", depends: []string{"b", "c"}},
			)
			state := make([]cuetry.StepRunState, len(sg.IndexToID))
			var stateMu sync.Mutex
			var mu sync.Mutex
			order := map[string]int{}
			counts := map[string]int{}
			seq := 0
			runStep := func(_ context.Context, i int) {
				id := sg.IndexToID[i]
				mu.Lock()
				seq++
				order[id] = seq
				counts[id]++
				mu.Unlock()
				stateMu.Lock()
				state[i] = cuetry.StepRunSucceeded
				stateMu.Unlock()
			}
			runGraphDataflow(context.Background(), sg, state, r, parallelism, &stateMu, runStep)

			for _, id := range []string{"a", "b", "c", "d"} {
				if counts[id] != 1 {
					t.Errorf("%s ran %d times, want 1", id, counts[id])
				}
			}
			if order["a"] >= order["b"] || order["a"] >= order["c"] {
				t.Errorf("a must start before b,c: %v", order)
			}
			if order["b"] >= order["d"] || order["c"] >= order["d"] {
				t.Errorf("b,c must start before d: %v", order)
			}
		})
	}
}

// TestDataflow_TriggerRuleOneFailed: a one_failed step runs iff a dependency
// failed, and is skipped otherwise.
func TestDataflow_TriggerRuleOneFailed(t *testing.T) {
	run := func(aFails bool) (bRan bool, bState cuetry.StepRunState) {
		sg, r := buildGraph(t,
			stepSpec{id: "a"},
			stepSpec{id: "b", depends: []string{"a"}, trigger: "one_failed"},
		)
		idx := sg.IDToIndex
		state := make([]cuetry.StepRunState, len(sg.IndexToID))
		var stateMu sync.Mutex
		var ran atomic.Bool
		runStep := func(_ context.Context, i int) {
			st := cuetry.StepRunSucceeded
			if i == idx["a"] && aFails {
				st = cuetry.StepRunFailed
			}
			if i == idx["b"] {
				ran.Store(true)
			}
			stateMu.Lock()
			state[i] = st
			stateMu.Unlock()
		}
		runGraphDataflow(context.Background(), sg, state, r, 2, &stateMu, runStep)
		return ran.Load(), state[idx["b"]]
	}

	if ran, st := run(true); !ran || st != cuetry.StepRunSucceeded {
		t.Errorf("dep failed: b ran=%v state=%v, want ran + Succeeded", ran, st)
	}
	if ran, st := run(false); ran || st != cuetry.StepRunSkipped {
		t.Errorf("dep ok: b ran=%v state=%v, want not-run + Skipped", ran, st)
	}
}

// TestDataflow_TriggerRuleAllDoneRunsAfterFailedDep: an all_done step runs even
// when its dependency failed (as long as the dependency is done).
func TestDataflow_TriggerRuleAllDoneRunsAfterFailedDep(t *testing.T) {
	sg, r := buildGraph(t,
		stepSpec{id: "a"},
		stepSpec{id: "b", depends: []string{"a"}, trigger: "all_done"},
	)
	idx := sg.IDToIndex
	state := make([]cuetry.StepRunState, len(sg.IndexToID))
	var stateMu sync.Mutex
	var bRan atomic.Bool
	runStep := func(_ context.Context, i int) {
		st := cuetry.StepRunSucceeded
		if i == idx["a"] {
			st = cuetry.StepRunFailed
		}
		if i == idx["b"] {
			bRan.Store(true)
		}
		stateMu.Lock()
		state[i] = st
		stateMu.Unlock()
	}
	runGraphDataflow(context.Background(), sg, state, r, 2, &stateMu, runStep)

	if !bRan.Load() {
		t.Error("all_done step b should run after a failed dependency")
	}
	if state[idx["b"]] != cuetry.StepRunSucceeded {
		t.Errorf("b = %v, want Succeeded", state[idx["b"]])
	}
}

// TestDataflow_RescueTarget: a rescue target runs iff the rescued step failed.
func TestDataflow_RescueTarget(t *testing.T) {
	run := func(aFails bool) (rcRan bool, rcState cuetry.StepRunState) {
		sg, r := buildGraph(t,
			stepSpec{id: "a", rescue: []string{"rc"}},
			stepSpec{id: "rc"},
		)
		idx := sg.IDToIndex
		state := make([]cuetry.StepRunState, len(sg.IndexToID))
		var stateMu sync.Mutex
		var ran atomic.Bool
		runStep := func(_ context.Context, i int) {
			st := cuetry.StepRunSucceeded
			if i == idx["a"] && aFails {
				st = cuetry.StepRunFailed
			}
			if i == idx["rc"] {
				ran.Store(true)
			}
			stateMu.Lock()
			state[i] = st
			stateMu.Unlock()
		}
		runGraphDataflow(context.Background(), sg, state, r, 2, &stateMu, runStep)
		return ran.Load(), state[idx["rc"]]
	}

	if ran, st := run(true); !ran || st != cuetry.StepRunSucceeded {
		t.Errorf("rescued step failed: rescue ran=%v state=%v, want ran + Succeeded", ran, st)
	}
	if ran, st := run(false); ran || st != cuetry.StepRunSkipped {
		t.Errorf("rescued step ok: rescue ran=%v state=%v, want not-run + Skipped", ran, st)
	}
}

// TestDataflow_AbortStopsQueueOnCancel: once the context is cancelled, the
// scheduler returns promptly and does not launch steps that were not yet
// running — even ones already made ready. "a" succeeds after cancel, so "b"
// is eligible, but the worker's ctx pre-check must decline to start it.
func TestDataflow_AbortStopsQueueOnCancel(t *testing.T) {
	sg, r := buildGraph(t,
		stepSpec{id: "a"},
		stepSpec{id: "b", depends: []string{"a"}},
	)
	idx := sg.IDToIndex
	state := make([]cuetry.StepRunState, len(sg.IndexToID))
	var stateMu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aStarted := make(chan struct{})
	var bRan atomic.Bool
	runStep := func(rctx context.Context, i int) {
		switch i {
		case idx["a"]:
			close(aStarted)
			<-rctx.Done() // block until cancelled, then complete successfully
		case idx["b"]:
			bRan.Store(true)
		}
		stateMu.Lock()
		state[i] = cuetry.StepRunSucceeded
		stateMu.Unlock()
	}

	schedDone := make(chan struct{})
	go func() {
		runGraphDataflow(ctx, sg, state, r, 4, &stateMu, runStep)
		close(schedDone)
	}()

	<-aStarted
	cancel() // a completes → b becomes ready → workers must decline to start it

	select {
	case <-schedDone:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not return after cancel")
	}
	if bRan.Load() {
		t.Error("b ran after cancel; abort should stop the queue")
	}
	if state[idx["b"]] != cuetry.StepRunPending {
		t.Errorf("b = %v, want Pending (never scheduled after cancel)", state[idx["b"]])
	}
}

// itoa avoids importing strconv for a single small conversion in subtests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(b[pos:])
}

// --- benchmark: wave barrier vs dataflow ------------------------------------

// benchRunWave reproduces the former wave-barrier scheduler (batch each wave,
// run concurrently bounded by a semaphore, full wg.Wait barrier before the next
// wave) so the benchmark can compare it against runGraphDataflow on the SAME
// injected step runner. Kept in the test file only.
func benchRunWave(ctx context.Context, sg *cuetry.StepGraph, state []cuetry.StepRunState, r *cuetry.Recipe, parallelism int, stateMu *sync.Mutex, runStep func(context.Context, int)) {
	for _, wave := range sg.Waves {
		var batch []int
		stateMu.Lock()
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
		stateMu.Unlock()
		if len(batch) == 0 {
			continue
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, parallelism)
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
				runStep(ctx, idx)
			}(idx)
		}
		wg.Wait()
	}
}

// BenchmarkGraphSchedule compares wall-clock on an uneven DAG where a slow
// off-critical-path step (b1, 20ms) shares a wave with the critical chain
// a1→a2→a3→a4 (5ms each). The wave scheduler stalls the whole a-chain behind
// b1 at its wave barrier; the dataflow scheduler lets the chain proceed.
func BenchmarkGraphSchedule(b *testing.B) {
	sg, r := buildGraph(b,
		stepSpec{id: "a1"},
		stepSpec{id: "a2", depends: []string{"a1"}},
		stepSpec{id: "a3", depends: []string{"a2"}},
		stepSpec{id: "a4", depends: []string{"a3"}},
		stepSpec{id: "b1", depends: []string{"a1"}},
	)
	dur := map[string]time.Duration{
		"a1": 5 * time.Millisecond,
		"a2": 5 * time.Millisecond,
		"a3": 5 * time.Millisecond,
		"a4": 5 * time.Millisecond,
		"b1": 20 * time.Millisecond,
	}
	makeRunStep := func(state []cuetry.StepRunState, mu *sync.Mutex) func(context.Context, int) {
		return func(_ context.Context, i int) {
			time.Sleep(dur[sg.IndexToID[i]])
			mu.Lock()
			state[i] = cuetry.StepRunSucceeded
			mu.Unlock()
		}
	}

	b.Run("wave", func(b *testing.B) {
		for b.Loop() {
			state := make([]cuetry.StepRunState, len(sg.IndexToID))
			var mu sync.Mutex
			benchRunWave(context.Background(), sg, state, r, 8, &mu, makeRunStep(state, &mu))
		}
	})
	b.Run("dataflow", func(b *testing.B) {
		for b.Loop() {
			state := make([]cuetry.StepRunState, len(sg.IndexToID))
			var mu sync.Mutex
			runGraphDataflow(context.Background(), sg, state, r, 8, &mu, makeRunStep(state, &mu))
		}
	})
}
