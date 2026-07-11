package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func testTargets(names ...string) []TargetContext {
	targets := make([]TargetContext, 0, len(names))
	for _, n := range names {
		targets = append(targets, TargetContext{Record: hosts.Record{Name: n, PrimaryIP: "10.0.0.1", Provider: "test"}})
	}
	return targets
}

func TestDispatchHostResults_CallsFnForEveryTarget(t *testing.T) {
	targets := testTargets("a", "b", "c")
	var mu sync.Mutex
	seen := map[string]bool{}
	var results []HostExecResult

	DispatchHostResults(context.Background(), targets, 0, 8, func(tc TargetContext) HostExecResult {
		mu.Lock()
		seen[tc.Record.Name] = true
		mu.Unlock()
		return HostExecResult{Name: tc.Record.Name, Success: true}
	}, func(res HostExecResult) {
		mu.Lock()
		results = append(results, res)
		mu.Unlock()
	})

	if len(seen) != 3 || !seen["a"] || !seen["b"] || !seen["c"] {
		t.Fatalf("expected fn called for all 3 targets, got %v", seen)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results sunk, got %d", len(results))
	}
}

func TestDispatchHostResults_ZeroMaxConcUsesDefault(t *testing.T) {
	targets := testTargets("a")
	called := false
	DispatchHostResults(context.Background(), targets, 0, 8, func(tc TargetContext) HostExecResult {
		called = true
		return HostExecResult{Name: tc.Record.Name, Success: true}
	}, func(HostExecResult) {})
	if !called {
		t.Fatal("expected fn to run with maxConc<=0 falling back to the default")
	}
}

func TestDispatchHostResults_ClampsAboveCap(t *testing.T) {
	targets := testTargets("a")
	var observedMax int64
	var current int64
	DispatchHostResults(context.Background(), targets, maxConcurrencyCap+1000, 8, func(tc TargetContext) HostExecResult {
		n := atomic.AddInt64(&current, 1)
		for {
			old := atomic.LoadInt64(&observedMax)
			if n <= old || atomic.CompareAndSwapInt64(&observedMax, old, n) {
				break
			}
		}
		atomic.AddInt64(&current, -1)
		return HostExecResult{Name: tc.Record.Name, Success: true}
	}, func(HostExecResult) {})
	if observedMax > maxConcurrencyCap {
		t.Fatalf("observed concurrency %d exceeds maxConcurrencyCap %d", observedMax, maxConcurrencyCap)
	}
}

func TestDispatchHostResults_CancelledContextSynthesizesFailureWithoutCallingFn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before dispatch starts

	targets := testTargets("a", "b")
	fnCalled := int32(0)
	var mu sync.Mutex
	var results []HostExecResult

	DispatchHostResults(ctx, targets, 8, 8, func(tc TargetContext) HostExecResult {
		atomic.AddInt32(&fnCalled, 1)
		return HostExecResult{Name: tc.Record.Name, Success: true}
	}, func(res HostExecResult) {
		mu.Lock()
		results = append(results, res)
		mu.Unlock()
	})

	if atomic.LoadInt32(&fnCalled) != 0 {
		t.Fatalf("expected fn never called on an already-cancelled context, got %d calls", fnCalled)
	}
	if len(results) != 2 {
		t.Fatalf("expected a synthesized failure result per target, got %d", len(results))
	}
	for _, res := range results {
		if res.Success {
			t.Fatalf("expected Success=false for a cancelled dispatch, got %+v", res)
		}
		if res.ErrMsg == "" {
			t.Fatalf("expected a non-empty ErrMsg for a cancelled dispatch, got %+v", res)
		}
		if res.Name == "" || res.IP == "" || res.Provider == "" {
			t.Fatalf("expected synthesized result to carry target identity, got %+v", res)
		}
	}
}

func TestDispatchHostResults_EmptyTargetsNoop(t *testing.T) {
	called := false
	DispatchHostResults(context.Background(), nil, 8, 8, func(TargetContext) HostExecResult {
		called = true
		return HostExecResult{}
	}, func(HostExecResult) {})
	if called {
		t.Fatal("expected fn never called with no targets")
	}
}
