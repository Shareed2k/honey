package engine

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

// TestStreamSSHParallel_CancelledCtxEmitsCancelled verifies that once the run
// context is cancelled, hosts are not dialed and each emits a "cancelled" result.
func TestStreamSSHParallel_CancelledCtxEmitsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the run starts

	jobs := []TargetContext{
		{Record: hosts.Record{Name: "h1", PrimaryIP: "10.0.0.1"}},
		{Record: hosts.Record{Name: "h2", PrimaryIP: "10.0.0.2"}},
	}

	ch := make(chan HostExecResult, len(jobs))
	go func() {
		defer close(ch)
		_ = StreamSSHParallel(ctx, "user", jobs, false,
			func(_ TargetContext, _ map[string]string) string { return "echo should-not-run" },
			ch, BatchOptions{})
	}()

	got := 0
	for r := range ch {
		got++
		if r.Success || r.ErrMsg != "cancelled" {
			t.Errorf("expected cancelled result, got %+v", r)
		}
	}
	if got != len(jobs) {
		t.Fatalf("expected %d results, got %d", len(jobs), got)
	}
}
