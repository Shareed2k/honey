//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
)

// runOne streams a single-host command via the real SSH container and returns
// the result, failing if it doesn't complete within wall.
func runOne(t *testing.T, ctx context.Context, reg *testRegistry, rec hosts.Record, cmd string, opts engine.BatchOptions, wall time.Duration) engine.HostExecResult {
	t.Helper()
	opts.Reg = reg
	ch := make(chan engine.HostExecResult, 1)
	go func() {
		defer close(ch)
		_ = engine.StreamCommandParallel(ctx, "testuser", []engine.TargetContext{{Record: rec}}, false,
			func(_ engine.TargetContext, _ map[string]string) string { return cmd }, ch, opts)
	}()

	var res engine.HostExecResult
	got := false
	timer := time.NewTimer(wall)
	defer timer.Stop()
	for {
		select {
		case r, ok := <-ch:
			if !ok {
				if !got {
					t.Fatal("no result emitted")
				}
				return res
			}
			res, got = r, true
		case <-timer.C:
			t.Fatalf("run did not finish within %s (timeout/cancel not enforced)", wall)
		}
	}
}

func TestExecCommandTimeout(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)
	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)
	reg := &testRegistry{Dialer: newTestDialer(sshH, sshP, keyFile)}

	res := runOne(t, context.Background(), reg, rec, "sleep 30",
		engine.BatchOptions{CmdTimeout: 2 * time.Second}, 10*time.Second)

	if res.Success {
		t.Fatalf("expected failure for timed-out command, got success: %+v", res)
	}
	if !strings.Contains(res.ErrMsg, "timed out") {
		t.Fatalf("expected timeout error, got %q", res.ErrMsg)
	}
}

func TestExecCancel(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)
	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)
	reg := &testRegistry{Dialer: newTestDialer(sshH, sshP, keyFile)}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(1*time.Second, cancel)
	defer cancel()

	res := runOne(t, ctx, reg, rec, "sleep 30", engine.BatchOptions{}, 10*time.Second)

	if res.Success {
		t.Fatalf("expected failure for cancelled command, got success: %+v", res)
	}
	if !strings.Contains(res.ErrMsg, "cancel") {
		t.Fatalf("expected cancellation error, got %q", res.ErrMsg)
	}
}
