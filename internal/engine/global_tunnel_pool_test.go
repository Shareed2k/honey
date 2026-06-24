package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestGlobalTunnelPool_acquireRelease ...
func TestGlobalTunnelPool_acquireRelease(t *testing.T) {
	t.Parallel()
	pool := NewGlobalTunnelPool(time.Minute)
	defer pool.Close()

	var started int32
	factory := func(_ context.Context) (TunnelEndpoint, func(), error) {
		atomic.AddInt32(&started, 1)
		return TunnelEndpoint{Host: "127.0.0.1", Port: 15432, Mode: "local"}, func() {}, nil
	}

	ep1, release1, err := pool.Acquire(context.Background(), "test-key", factory)
	if err != nil {
		t.Fatal(err)
	}
	if ep1.Port != 15432 || atomic.LoadInt32(&started) != 1 {
		t.Fatalf("ep=%+v started=%d", ep1, started)
	}

	ep2, release2, err := pool.Acquire(context.Background(), "test-key", factory)
	if err != nil {
		t.Fatal(err)
	}
	if ep2.Port != 15432 || atomic.LoadInt32(&started) != 1 {
		t.Fatalf("reuse failed: ep=%+v started=%d", ep2, started)
	}

	release1()
	release2()
}

// TestGlobalTunnelPool_factoryError ...
func TestGlobalTunnelPool_factoryError(t *testing.T) {
	t.Parallel()
	pool := NewGlobalTunnelPool(time.Minute)
	defer pool.Close()

	_, _, err := pool.Acquire(context.Background(), "err-key", func(_ context.Context) (TunnelEndpoint, func(), error) {
		return TunnelEndpoint{}, nil, context.Canceled
	})
	if err == nil {
		t.Fatal("expected error")
	}

	ep, release, err := pool.Acquire(context.Background(), "err-key", func(_ context.Context) (TunnelEndpoint, func(), error) {
		return TunnelEndpoint{Host: "127.0.0.1", Port: 9999, Mode: "local"}, func() {}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Port != 9999 {
		t.Fatalf("got %+v", ep)
	}
	release()
}

// TestTunnelLookupKeyForShare ...
func TestTunnelLookupKeyForShare(t *testing.T) {
	t.Parallel()
	if got := TunnelLookupKeyForShare("db-pg", "derived"); got != "share:db-pg" {
		t.Fatalf("got %q", got)
	}
	if got := TunnelLookupKeyForShare("", "abc"); got != "derived:abc" {
		t.Fatalf("got %q", got)
	}
}
