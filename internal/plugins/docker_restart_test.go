package plugins

import (
	"context"
	"errors"
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
