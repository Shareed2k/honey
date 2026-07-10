package plugins

import (
	"context"

	backoff "github.com/cenkalti/backoff/v5"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"go.uber.org/zap"
)

// createAndStartFunc matches createAndStart's signature — a seam so tests
// can simulate create/start failures without a real Docker daemon.
type createAndStartFunc func(ctx context.Context) (containerID, addr string, err error)

// startWatching spawns the crash-watch goroutine. Called once from
// newDockerTransport after the initial container is up.
//
//nolint:unused // called by newDockerTransport, which is wired in by Task 7 (not yet landed on this branch)
func (t *dockerTransport) startWatching(ctx context.Context) {
	t.mu.Lock()
	if t.stopWatch == nil {
		t.stopWatch = make(chan struct{})
	}
	t.mu.Unlock()
	go t.watchLoop(ctx)
}

//nolint:unused // called by startWatching's goroutine, which is wired in by Task 7 (not yet landed on this branch)
func (t *dockerTransport) watchLoop(ctx context.Context) {
	for {
		t.mu.RLock()
		id := t.containerID
		stop := t.stopWatch
		t.mu.RUnlock()

		waitResult := t.cli.ContainerWait(ctx, id, client.ContainerWaitOptions{
			Condition: containertypes.WaitConditionNotRunning,
		})

		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case err := <-waitResult.Error:
			if err != nil && ctx.Err() == nil {
				zap.L().Warn("plugins: docker container wait error", zap.String("container_id", id), zap.Error(err))
			}
		case res := <-waitResult.Result:
			if ctx.Err() != nil {
				return // Manager is shutting down; this exit is expected.
			}
			zap.L().Warn("plugins: docker plugin container exited unexpectedly, restarting",
				zap.String("container_id", id), zap.Int64("exit_code", res.StatusCode))
			t.restart(ctx, func(ctx context.Context) (string, string, error) {
				return createAndStart(ctx, t.cli, t.createCfg)
			})
			if t.isRestarting() {
				// restart only leaves restarting=true when it gave up
				// because ctx is done (Manager shutting down) — nothing
				// left to watch.
				return
			}
			// restart succeeded: loop again, now watching the new container.
		}
	}
}

// restart recreates the container with exponential backoff (capped at
// createCfg.MaxBackoff, unbounded retries — never permanently give up).
// createFn is a seam for testing; production callers always pass a closure
// over createAndStart. restart itself never spawns a goroutine — its sole
// caller, watchLoop, resumes watching the new container in its own loop on
// success (startWatching spawns the one and only crash-watch goroutine).
func (t *dockerTransport) restart(ctx context.Context, createFn createAndStartFunc) {
	t.setRestarting(true)

	b := backoff.NewExponentialBackOff()
	if t.createCfg.MaxBackoff > 0 {
		b.MaxInterval = t.createCfg.MaxBackoff
	}

	attempt := 0
	operation := func() (struct{ id, addr string }, error) {
		attempt++
		id, addr, err := createFn(ctx)
		if err != nil {
			zap.L().Warn("plugins: docker plugin restart attempt failed", zap.Int("attempt", attempt), zap.Error(err))
			return struct{ id, addr string }{}, err
		}
		return struct{ id, addr string }{id, addr}, nil
	}

	result, err := backoff.Retry(ctx, operation, backoff.WithBackOff(b), backoff.WithMaxElapsedTime(0))
	if err != nil {
		// Only reachable if ctx itself is done (WithMaxElapsedTime(0) means
		// "never give up" otherwise) — Manager is shutting down.
		zap.L().Error("plugins: docker plugin restart aborted", zap.Error(err))
		return
	}

	t.mu.Lock()
	t.containerID = result.id
	t.addr = result.addr
	t.restarting = false
	t.mu.Unlock()
}
