package plugins

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"
)

// concurrencyProbeTransport records the peak number of in-flight CallRaw calls,
// so a test can tell whether the manager serialized or overlapped them.
type concurrencyProbeTransport struct {
	active    atomic.Int64
	maxActive atomic.Int64
	delay     time.Duration
}

func (t *concurrencyProbeTransport) CallRaw(_ context.Context, _ string, _ []byte) (int, []byte, error) {
	n := t.active.Add(1)
	for {
		m := t.maxActive.Load()
		if n <= m || t.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(t.delay)
	t.active.Add(-1)
	return 0, []byte("{}"), nil
}

func (t *concurrencyProbeTransport) Close(context.Context) error { return nil }

func callN(m *Manager, id string, n int) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Call(context.Background(), id, "diagnose", map[string]any{}, nil)
		}()
	}
	wg.Wait()
}

func TestManagerCall_dockerCallsOverlap(t *testing.T) {
	tr := &concurrencyProbeTransport{delay: 20 * time.Millisecond}
	lp := &loadedPlugin{
		manifest:  Manifest{ID: "docker-plugin"},
		transport: tr,
		callSem:   semaphore.NewWeighted(maxConcurrentDockerPluginCalls),
	}
	m := &Manager{enabled: true, timeoutMS: 30000, byID: map[string]*loadedPlugin{"docker-plugin": lp}}

	callN(m, "docker-plugin", 8)
	if got := tr.maxActive.Load(); got < 2 {
		t.Fatalf("docker plugin calls did not overlap: peak concurrency=%d, want >=2", got)
	}
	if got := tr.maxActive.Load(); got > maxConcurrentDockerPluginCalls {
		t.Fatalf("exceeded bound: peak=%d > %d", got, maxConcurrentDockerPluginCalls)
	}
}

func TestManagerCall_wasmCallsSerialize(t *testing.T) {
	tr := &concurrencyProbeTransport{delay: 5 * time.Millisecond}
	lp := &loadedPlugin{
		manifest:  Manifest{ID: "wasm-plugin"},
		transport: tr,
		// callSem nil → WASM path uses callMu (extism is not concurrent-safe).
	}
	m := &Manager{enabled: true, timeoutMS: 30000, byID: map[string]*loadedPlugin{"wasm-plugin": lp}}

	callN(m, "wasm-plugin", 8)
	if got := tr.maxActive.Load(); got != 1 {
		t.Fatalf("wasm plugin calls overlapped: peak concurrency=%d, want 1", got)
	}
}
