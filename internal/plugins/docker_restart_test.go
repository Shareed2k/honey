package plugins

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

func TestDockerTransport_CallRaw_FailsFastWhileRestarting(t *testing.T) {
	dt := newTestDockerTransport(t, "http://unused.invalid", testDockerCueSource)
	dt.setRestarting(true)

	_, _, err := dt.CallRaw(context.Background(), "scan", []byte(`{"target":"x"}`))
	if err == nil {
		t.Fatal("expected error while restarting")
	}
}

func TestDockerTransport_CallRaw_SucceedsOnceNotRestarting(t *testing.T) {
	srv := newFakePluginInitServer(t, func(_ apiv1.ExecRequest) apiv1.ExecResponse {
		return apiv1.ExecResponse{Output: `{"vulnerabilities":[]}`, ExitCode: 0}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)
	dt.setRestarting(false)

	_, _, err := dt.CallRaw(context.Background(), "scan", []byte(`{"target":"x"}`))
	if err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
}

// fakeContainerRestarter lets restartLoop's retry logic be tested without a
// real Docker daemon: succeeds on the Nth attempt.
type fakeContainerRestarter struct {
	failuresBeforeSuccess int
	attempts              int
}

func (f *fakeContainerRestarter) createAndStart(context.Context) (string, string, error) {
	f.attempts++
	if f.attempts <= f.failuresBeforeSuccess {
		return "", "", errors.New("simulated create failure")
	}
	return "new-container-id", "http://new-addr.invalid:49094", nil
}

func TestDockerTransport_Restart_RetriesUntilSuccess(t *testing.T) {
	dt := newTestDockerTransport(t, "http://old-addr.invalid", testDockerCueSource)
	dt.containerID = "old-container-id"
	dt.createCfg.MaxBackoff = 10 * time.Millisecond

	restarter := &fakeContainerRestarter{failuresBeforeSuccess: 2}
	dt.restart(context.Background(), restarter.createAndStart)

	if restarter.attempts != 3 {
		t.Fatalf("attempts=%d want 3 (2 failures + 1 success)", restarter.attempts)
	}
	if dt.containerID != "new-container-id" {
		t.Fatalf("containerID=%q want new-container-id", dt.containerID)
	}
	if dt.addr != "http://new-addr.invalid:49094" {
		t.Fatalf("addr=%q", dt.addr)
	}
	if dt.isRestarting() {
		t.Fatal("expected restarting=false after successful restart")
	}
}

// TestDockerTransport_Close_CancelsInProgressRestartBackoff proves that
// cancelling the transport's own internally-derived context (dt.cancel,
// wired up in newDockerTransport and invoked by Close) interrupts an
// in-progress restart backoff loop, even when the caller's original context
// (simulated here by context.Background()) is never cancelled on its own.
// Before the fix, Close only closed stopWatch — which watchLoop's idle
// select observes, but restart's backoff.Retry does not — so a
// crash-triggered restart with a never-cancelled caller ctx would retry
// forever, uninterrupted by Close.
//
// This test exercises the cancellation mechanism itself (internalCtx ->
// watchLoop -> restart -> backoff.Retry) by calling dt.cancel() directly,
// deliberately not calling dt.Close(). dt has no real *client.Client here on
// purpose: internal/plugins's unit suite must have zero real-Docker-daemon
// dependency, and Close's t.cli.ContainerStop/ContainerRemove calls are
// unbounded real network I/O unrelated to what this test is proving — if
// DOCKER_HOST pointed at an unreachable endpoint in some environment, that
// path could hang far longer than this test's bound and make the failure
// about the wrong thing.
func TestDockerTransport_Close_CancelsInProgressRestartBackoff(t *testing.T) {
	dt := newTestDockerTransport(t, "http://old-addr.invalid", testDockerCueSource)
	dt.createCfg.MaxBackoff = 5 * time.Millisecond

	// Simulate newDockerTransport's wiring: the transport owns its own
	// cancellable context, derived from (but independent of) whatever
	// context the caller passed in at construction — which in production
	// may be context.Background() and thus never cancel on its own.
	callerCtx := context.Background()
	internalCtx, cancel := context.WithCancel(callerCtx)
	dt.cancel = cancel

	// A plain fakeContainerRestarter's attempts counter isn't safe to read
	// from the test goroutine while restart's goroutine is concurrently
	// writing it (that fixture is only ever used synchronously elsewhere),
	// so this test tracks attempts with its own atomic counter instead.
	var attempts atomic.Int64
	createFn := func(context.Context) (string, string, error) {
		attempts.Add(1)
		return "", "", errors.New("simulated create failure")
	}

	done := make(chan struct{})
	go func() {
		dt.restart(internalCtx, createFn)
		close(done)
	}()

	// Give the backoff loop a moment to actually get into a retry cycle
	// before we cancel.
	time.Sleep(30 * time.Millisecond)
	if attempts.Load() == 0 {
		t.Fatal("expected at least one restart attempt to have happened before cancelling")
	}

	// This is exactly what the fixed Close does: cancel the transport's own
	// derived context (dt.cancel), independent of the caller's ctx.
	dt.cancel()

	select {
	case <-done:
		// restart() returned promptly because cancelling the transport's
		// derived context unblocked backoff.Retry — the fix under test.
	case <-time.After(2 * time.Second):
		t.Fatal("restart() backoff loop did not stop within 2s of cancelling dt's derived context, even though the caller's own context was never cancelled")
	}
}
