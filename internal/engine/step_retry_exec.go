package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/shareed2k/honey/internal/cuetry"
	"go.uber.org/zap"
)

var errStepRetryable = errors.New("step retryable failure")

// HostExecRetryOutcome is the result of RunHostExecWithRetry.
// HostExecRetryOutcome ...
type HostExecRetryOutcome struct {
	Result              HostExecResult
	Attempts            int
	LastAttemptDuration time.Duration
}

// RunHostExecWithRetry ...
func RunHostExecWithRetry(ctx context.Context, cfg cuetry.RecipeStepRetry, run func() HostExecResult) HostExecRetryOutcome {
	start := time.Now()
	if !cfg.Enabled() {
		return HostExecRetryOutcome{Result: run(), Attempts: 1, LastAttemptDuration: time.Since(start)}
	}
	var attempt atomic.Uint32
	var lastAttemptDuration time.Duration

	opts := []backoff.RetryOption{
		backoff.WithMaxTries(uint(cfg.Attempts)),
		backoff.WithBackOff(cuetry.BuildBackOff(cfg)),
		backoff.WithNotify(func(err error, next time.Duration) {
			zap.L().Debug("step retry waiting",
				zap.Uint32("attempt", attempt.Load()),
				zap.Int("max_attempts", cfg.Attempts),
				zap.Duration("next", next),
				zap.Error(err),
			)
		}),
	}

	res, err := backoff.Retry(ctx, func() (HostExecResult, error) {
		attemptStart := time.Now()
		n := attempt.Add(1)
		out := run()
		lastAttemptDuration = time.Since(attemptStart)
		if cuetry.ShouldRetryHostResult(out.Success, out.Skipped) {
			out.Output = appendRetryNote(out.Output, int(n), cfg.Attempts)
			return out, fmt.Errorf("%w: %s", errStepRetryable, retryErrMsg(out))
		}
		return out, nil
	}, opts...)
	if err != nil && res.Name == "" && res.IP == "" && res.Output == "" && res.ErrMsg == "" {
		attemptStart := time.Now()
		out := run()
		lastAttemptDuration = time.Since(attemptStart)
		if !out.Success {
			out.Output = appendRetryNote(out.Output, cfg.Attempts, cfg.Attempts)
		}
		return HostExecRetryOutcome{Result: out, Attempts: cfg.Attempts, LastAttemptDuration: lastAttemptDuration}
	}
	attempts := int(attempt.Load())
	if attempts == 0 {
		attempts = 1
	}
	return HostExecRetryOutcome{Result: res, Attempts: attempts, LastAttemptDuration: lastAttemptDuration}
}

func retryErrMsg(res HostExecResult) string {
	if res.ErrMsg != "" {
		return res.ErrMsg
	}
	if res.ExitCode != 0 {
		return fmt.Sprintf("exit %d", res.ExitCode)
	}
	return "step failed"
}

// WriteCueStepRetryDryLine prints retry settings when enabled.
// WriteCueStepRetryDryLine ...
func WriteCueStepRetryDryLine(out io.Writer, stepIdx int, cfg cuetry.RecipeStepRetry) {
	if !cfg.Enabled() {
		return
	}
	_, _ = fmt.Fprintf(out, "  step %d retry: attempts=%d delay_ms=%d max_delay_ms=%d backoff=%s\n",
		stepIdx+1, cfg.Attempts, cfg.DelayMS, cfg.MaxDelayMS, cfg.Backoff)
}

func appendRetryNote(output string, attempt, maxAttempts int) string {
	note := fmt.Sprintf("(retry %d/%d)", attempt, maxAttempts)
	output = strings.TrimSpace(output)
	if output == "" {
		return note
	}
	return output + "\n" + note
}
