package cuetry

import (
	"testing"
	"time"

	"github.com/cenkalti/backoff/v5"
)

func TestEffectiveRetryMerge(t *testing.T) {
	t.Parallel()
	defaults := &RecipeDefaults{
		Retry: &RecipeStepRetry{Attempts: 5, DelayMS: 500, Backoff: "exponential"},
	}
	step := RecipeStep{
		Retry: &RecipeStepRetry{Attempts: 10},
	}
	got := EffectiveRetry(step, defaults)
	if got.Attempts != 10 {
		t.Fatalf("attempts: got %d want 10", got.Attempts)
	}
	if got.DelayMS != 500 {
		t.Fatalf("delay_ms: got %d want 500", got.DelayMS)
	}
	if got.Backoff != "exponential" {
		t.Fatalf("backoff: got %q want exponential", got.Backoff)
	}
}

func TestEffectiveRetryDefaultsOnly(t *testing.T) {
	t.Parallel()
	defaults := &RecipeDefaults{
		Retry: &RecipeStepRetry{Attempts: 4},
	}
	got := EffectiveRetry(RecipeStep{}, defaults)
	if got.Attempts != 4 {
		t.Fatalf("attempts: got %d want 4", got.Attempts)
	}
	if got.DelayMS != defaultRetryDelayMS {
		t.Fatalf("delay_ms: got %d want %d", got.DelayMS, defaultRetryDelayMS)
	}
	if got.Backoff != "fixed" {
		t.Fatalf("backoff: got %q want fixed", got.Backoff)
	}
}

func TestEffectiveRetryDisabledWhenAbsent(t *testing.T) {
	t.Parallel()
	got := EffectiveRetry(RecipeStep{}, nil)
	if got.Enabled() {
		t.Fatal("expected retry disabled when no block")
	}
	if got.Attempts != 0 {
		t.Fatalf("attempts: got %d want 0", got.Attempts)
	}
}

func TestEffectiveRetrySingleAttempt(t *testing.T) {
	t.Parallel()
	step := RecipeStep{Retry: &RecipeStepRetry{Attempts: 1}}
	got := EffectiveRetry(step, nil)
	if got.Enabled() {
		t.Fatal("attempts=1 should not enable retry")
	}
}

func TestValidateRetry(t *testing.T) {
	t.Parallel()
	if err := ValidateRetry(RecipeStepRetry{Attempts: 2, Backoff: "fixed"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRetry(RecipeStepRetry{Attempts: 2, Backoff: "bad"}); err == nil {
		t.Fatal("expected error for bad backoff")
	}
	if err := ValidateRetry(RecipeStepRetry{Attempts: 2, DelayMS: 5000, MaxDelayMS: 1000, Backoff: "fixed"}); err == nil {
		t.Fatal("expected error when max_delay_ms < delay_ms")
	}
}

func TestBuildBackOffFixed(t *testing.T) {
	t.Parallel()
	bo := BuildBackOff(RecipeStepRetry{DelayMS: 2000, Backoff: "fixed"})
	if got := bo.NextBackOff(); got != 2*time.Second {
		t.Fatalf("got %v want 2s", got)
	}
}

func TestBuildBackOffExponential(t *testing.T) {
	t.Parallel()
	bo := BuildBackOff(RecipeStepRetry{DelayMS: 100, MaxDelayMS: 1000, Backoff: "exponential"})
	first := bo.NextBackOff()
	second := bo.NextBackOff()
	if first != 100*time.Millisecond {
		t.Fatalf("first: got %v", first)
	}
	if second <= first {
		t.Fatalf("expected increasing backoff, got %v then %v", first, second)
	}
	if _, ok := bo.(*backoff.ExponentialBackOff); !ok {
		t.Fatalf("expected ExponentialBackOff, got %T", bo)
	}
}

func TestShouldRetryHostResult(t *testing.T) {
	t.Parallel()
	if ShouldRetryHostResult(true, false) {
		t.Fatal("success should not retry")
	}
	if ShouldRetryHostResult(false, true) {
		t.Fatal("skipped should not retry")
	}
	if !ShouldRetryHostResult(false, false) {
		t.Fatal("failure should retry")
	}
}
