package cuetry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingResolver is a fake SecretResolver that counts backend hits and can
// simulate a slow/failing backend, to prove memoization avoids re-resolution.
type countingResolver struct {
	calls atomic.Int64
	delay time.Duration
	err   error
}

func (c *countingResolver) Handles(string) bool { return true }

func (c *countingResolver) Resolve(_ context.Context, ref string) (string, error) {
	c.calls.Add(1)
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	if c.err != nil {
		return "", c.err
	}
	return "val:" + ref, nil
}

func TestMemoizingResolver_cachesRepeated(t *testing.T) {
	t.Parallel()
	inner := &countingResolver{}
	m := newMemoizingResolver(inner)

	for i := 0; i < 50; i++ {
		v, err := m.Resolve(context.Background(), "secure:v1:x")
		if err != nil || v != "val:secure:v1:x" {
			t.Fatalf("resolve %d: %v %q", i, err, v)
		}
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("same ref resolved %d times, want 1 backend call", got)
	}
	// A distinct ref gets its own single backend call.
	if _, err := m.Resolve(context.Background(), "secure:v1:y"); err != nil {
		t.Fatal(err)
	}
	if got := inner.calls.Load(); got != 2 {
		t.Fatalf("after distinct ref, backend calls=%d, want 2", got)
	}
}

func TestMemoizingResolver_concurrentSameRefSingleflight(t *testing.T) {
	t.Parallel()
	inner := &countingResolver{delay: 20 * time.Millisecond}
	m := newMemoizingResolver(inner)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.Resolve(context.Background(), "secure:v1:z")
		}()
	}
	wg.Wait()
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("64 concurrent same-ref resolves → %d backend calls, want 1 (singleflight)", got)
	}
}

func TestMemoizingResolver_cachesError(t *testing.T) {
	t.Parallel()
	inner := &countingResolver{err: errors.New("backend down")}
	m := newMemoizingResolver(inner)
	for i := 0; i < 5; i++ {
		if _, err := m.Resolve(context.Background(), "bad"); err == nil {
			t.Fatal("expected error")
		}
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("error not cached: backend calls=%d, want 1", got)
	}
}

// BenchmarkSecretResolveRepeated compares resolving one ref `repeats` times (≈
// one secret used across hosts×steps) with and without the per-run memo. Each
// iteration uses a FRESH memoized resolver so the memo cost is per-run-realistic.
func BenchmarkSecretResolveRepeated(b *testing.B) {
	const repeats = 32
	const backendDelay = 50 * time.Microsecond
	ctx := context.Background()

	b.Run("raw", func(b *testing.B) {
		for b.Loop() {
			r := &countingResolver{delay: backendDelay}
			for i := 0; i < repeats; i++ {
				_, _ = r.Resolve(ctx, "secure:v1:x")
			}
		}
	})
	b.Run("memoized", func(b *testing.B) {
		for b.Loop() {
			r := newMemoizingResolver(&countingResolver{delay: backendDelay})
			for i := 0; i < repeats; i++ {
				_, _ = r.Resolve(ctx, "secure:v1:x")
			}
		}
	})
}
