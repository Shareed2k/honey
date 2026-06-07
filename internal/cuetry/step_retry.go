package cuetry

import (
	"fmt"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
)

const (
	defaultRetryAttempts   = 3
	defaultRetryDelayMS    = 1000
	defaultRetryMaxDelayMS = 30000
)

// RecipeStepRetry configures per-step retry for remote actions.
type RecipeStepRetry struct {
	Attempts   int    `json:"attempts,omitempty"`
	DelayMS    int    `json:"delay_ms,omitempty"`
	MaxDelayMS int    `json:"max_delay_ms,omitempty"`
	Backoff    string `json:"backoff,omitempty"`
}

// Enabled reports whether step retry is active (more than one total attempt).
func (r RecipeStepRetry) Enabled() bool {
	return r.Attempts > 1
}

// EffectiveRetry merges step and recipe defaults; applies defaults when a retry block is present.
func EffectiveRetry(step *StepBase, defaults *RecipeDefaults) RecipeStepRetry {
	var base *RecipeStepRetry
	if defaults != nil && defaults.Retry != nil {
		cp := *defaults.Retry
		base = &cp
	}
	if step.Retry != nil {
		out := RecipeStepRetry{}
		if base != nil {
			out = *base
		}
		if step.Retry.Attempts > 0 {
			out.Attempts = step.Retry.Attempts
		}
		if step.Retry.DelayMS > 0 {
			out.DelayMS = step.Retry.DelayMS
		}
		if step.Retry.MaxDelayMS > 0 {
			out.MaxDelayMS = step.Retry.MaxDelayMS
		}
		if s := strings.TrimSpace(step.Retry.Backoff); s != "" {
			out.Backoff = s
		}
		return normalizeRetry(out)
	}
	if base != nil {
		return normalizeRetry(*base)
	}
	return RecipeStepRetry{}
}

func normalizeRetry(r RecipeStepRetry) RecipeStepRetry {
	if r.Attempts <= 0 {
		r.Attempts = defaultRetryAttempts
	}
	if r.DelayMS <= 0 {
		r.DelayMS = defaultRetryDelayMS
	}
	if r.MaxDelayMS <= 0 {
		r.MaxDelayMS = defaultRetryMaxDelayMS
	}
	if s := strings.TrimSpace(r.Backoff); s == "" {
		r.Backoff = "fixed"
	} else {
		r.Backoff = s
	}
	return r
}

// ValidateRetry returns an error for invalid retry configuration.
func ValidateRetry(r RecipeStepRetry) error {
	if !r.Enabled() {
		return nil
	}
	switch r.Backoff {
	case "fixed", "exponential":
	default:
		return fmt.Errorf("retry.backoff must be fixed or exponential, got %q", r.Backoff)
	}
	if r.MaxDelayMS > 0 && r.DelayMS > 0 && r.MaxDelayMS < r.DelayMS {
		return fmt.Errorf("retry.max_delay_ms (%d) must be >= delay_ms (%d)", r.MaxDelayMS, r.DelayMS)
	}
	return nil
}

// BuildBackOff returns a backoff strategy for the given retry config.
func BuildBackOff(r RecipeStepRetry) backoff.BackOff {
	delay := time.Duration(r.DelayMS) * time.Millisecond
	maxDelay := time.Duration(r.MaxDelayMS) * time.Millisecond
	if r.Backoff == "exponential" {
		bo := backoff.NewExponentialBackOff()
		bo.InitialInterval = delay
		bo.MaxInterval = maxDelay
		return bo
	}
	return backoff.NewConstantBackOff(delay)
}

// ShouldRetryHostResult reports whether a host exec result should be retried.
func ShouldRetryHostResult(success, skipped bool) bool {
	return !success && !skipped
}
