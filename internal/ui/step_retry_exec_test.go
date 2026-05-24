package ui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
)

func TestRunHostExecWithRetryDisabled(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	cfg := cuetry.RecipeStepRetry{Attempts: 1}
	res := runHostExecWithRetry(context.Background(), cfg, func() HostExecResult {
		calls.Add(1)
		return HostExecResult{Name: "h1", Success: false}
	}).Result
	if calls.Load() != 1 {
		t.Fatalf("calls: got %d want 1", calls.Load())
	}
	if res.Name != "h1" || res.Success {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestRunHostExecWithRetryUntilSuccess(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	cfg := cuetry.RecipeStepRetry{Attempts: 5, DelayMS: 1, Backoff: "fixed"}
	res := runHostExecWithRetry(context.Background(), cfg, func() HostExecResult {
		n := calls.Add(1)
		if n < 3 {
			return HostExecResult{Name: "h1", Success: false, ErrMsg: "not yet"}
		}
		return HostExecResult{Name: "h1", Success: true, Output: "ok"}
	}).Result
	if calls.Load() != 3 {
		t.Fatalf("calls: got %d want 3", calls.Load())
	}
	if !res.Success || res.Output != "ok" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestRunHostExecWithRetryExhausted(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	cfg := cuetry.RecipeStepRetry{Attempts: 3, DelayMS: 1, Backoff: "fixed"}
	res := runHostExecWithRetry(context.Background(), cfg, func() HostExecResult {
		calls.Add(1)
		return HostExecResult{Name: "h1", Success: false, ErrMsg: "fail"}
	}).Result
	if calls.Load() != 3 {
		t.Fatalf("calls: got %d want 3", calls.Load())
	}
	if res.Success {
		t.Fatal("expected failure")
	}
	if !strings.Contains(res.Output, "(retry 3/3)") {
		t.Fatalf("expected retry note in output, got %q", res.Output)
	}
}

func TestRunHostExecWithRetryContextCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	cfg := cuetry.RecipeStepRetry{Attempts: 10, DelayMS: 500, Backoff: "fixed"}
	done := make(chan HostExecResult, 1)
	go func() {
		done <- runHostExecWithRetry(ctx, cfg, func() HostExecResult {
			n := calls.Add(1)
			if n == 1 {
				cancel()
			}
			return HostExecResult{Name: "h1", Success: false}
		}).Result
	}()
	select {
	case res := <-done:
		if res.Success {
			t.Fatal("expected failure")
		}
		if calls.Load() > 2 {
			t.Fatalf("expected early cancel, calls=%d", calls.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestRunHostExecWithRetrySkippedNotRetried(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	cfg := cuetry.RecipeStepRetry{Attempts: 5, DelayMS: 1, Backoff: "fixed"}
	res := runHostExecWithRetry(context.Background(), cfg, func() HostExecResult {
		calls.Add(1)
		return HostExecResult{Name: "h1", Skipped: true, Success: false}
	}).Result
	if calls.Load() != 1 {
		t.Fatalf("calls: got %d want 1", calls.Load())
	}
	if !res.Skipped {
		t.Fatal("expected skipped")
	}
}
