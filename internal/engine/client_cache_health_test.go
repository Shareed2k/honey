package engine

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
)

// healthCountingClient is a HostClient that also implements healthChecker and
// counts keepalive probes, so we can assert the cache throttles them.
type healthCountingClient struct {
	*FakeHostClient
	healthCalls atomic.Int64
}

func (h *healthCountingClient) Healthy() error {
	h.healthCalls.Add(1)
	return nil
}

func TestClientCache_keepaliveThrottled(t *testing.T) {
	saved := clientHealthCheckInterval
	t.Cleanup(func() { clientHealthCheckInterval = saved })

	c := NewClientCache()
	c.SetRegistry(&MockRegistry{})
	r := hosts.Record{Provider: "test", PrimaryIP: "127.0.0.1", Name: "h"}
	key := SSHClientCacheKey("u", r)
	hc := &healthCountingClient{FakeHostClient: &FakeHostClient{}}

	// Inject a cached client whose last probe is far in the past.
	c.mu.Lock()
	c.clients[key] = hc
	c.leases[key] = 0
	c.lastHealth[key] = time.Time{}
	c.mu.Unlock()

	clientHealthCheckInterval = time.Hour
	for i := 0; i < 5; i++ {
		if _, err := c.GetOrDial("u", r); err != nil {
			t.Fatalf("GetOrDial %d: %v", i, err)
		}
	}
	if got := hc.healthCalls.Load(); got != 1 {
		t.Fatalf("throttled: probed %d times across 5 hits, want 1", got)
	}
}

func TestClientCache_keepaliveEveryHitWhenIntervalZero(t *testing.T) {
	saved := clientHealthCheckInterval
	t.Cleanup(func() { clientHealthCheckInterval = saved })

	c := NewClientCache()
	c.SetRegistry(&MockRegistry{})
	r := hosts.Record{Provider: "test", PrimaryIP: "127.0.0.1", Name: "h2"}
	key := SSHClientCacheKey("u", r)
	hc := &healthCountingClient{FakeHostClient: &FakeHostClient{}}

	c.mu.Lock()
	c.clients[key] = hc
	c.leases[key] = 0
	c.lastHealth[key] = time.Time{}
	c.mu.Unlock()

	clientHealthCheckInterval = 0 // never fresh → probe every hit
	for i := 0; i < 3; i++ {
		if _, err := c.GetOrDial("u", r); err != nil {
			t.Fatalf("GetOrDial %d: %v", i, err)
		}
	}
	if got := hc.healthCalls.Load(); got != 3 {
		t.Fatalf("interval 0: probed %d times across 3 hits, want 3", got)
	}
}
