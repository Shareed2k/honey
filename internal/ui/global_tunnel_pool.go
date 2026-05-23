package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const defaultTunnelIdleTTL = 30 * time.Minute

// TunnelEndpoint describes an operator-side listen address for a recipe tunnel.
type TunnelEndpoint struct {
	Host       string
	Port       int
	Mode       string
	TunName    string
	RemoteHost string
	RemotePort int
	ShareKey   string
}

type tunnelPoolEntry struct {
	endpoint TunnelEndpoint
	stop     func()
	refcount int
	lastUsed time.Time
	ready    chan struct{}
	err      error
}

// GlobalTunnelPool caches active tunnels keyed by share_key or derived spec hash.
type GlobalTunnelPool struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]*tunnelPoolEntry
	closed  bool
	stopCh  chan struct{}
}

var defaultGlobalTunnelPool = NewGlobalTunnelPool(defaultTunnelIdleTTL)

// DefaultGlobalTunnelPool returns the process-wide tunnel pool.
func DefaultGlobalTunnelPool() *GlobalTunnelPool {
	return defaultGlobalTunnelPool
}

// NewGlobalTunnelPool creates a pool with the given idle TTL (0 = default 30m).
func NewGlobalTunnelPool(ttl time.Duration) *GlobalTunnelPool {
	if ttl <= 0 {
		ttl = defaultTunnelIdleTTL
	}
	p := &GlobalTunnelPool{
		ttl:     ttl,
		entries: make(map[string]*tunnelPoolEntry),
		stopCh:  make(chan struct{}),
	}
	go p.sweepLoop()
	return p
}

// Acquire returns an endpoint, creating via factory on miss. release() decrements refcount.
func (p *GlobalTunnelPool) Acquire(ctx context.Context, key string, factory func(context.Context) (TunnelEndpoint, func(), error)) (TunnelEndpoint, func(), error) {
	if p == nil {
		return TunnelEndpoint{}, nil, errors.New("tunnel pool: nil")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return TunnelEndpoint{}, nil, errors.New("tunnel pool: empty key")
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return TunnelEndpoint{}, nil, errors.New("tunnel pool: closed")
	}
	if ent, ok := p.entries[key]; ok {
		<-ent.ready
		if ent.err != nil {
			delete(p.entries, key)
			p.mu.Unlock()
			return TunnelEndpoint{}, nil, ent.err
		}
		ent.refcount++
		ent.lastUsed = time.Now()
		ep := ent.endpoint
		p.mu.Unlock()
		return ep, p.releaseFn(key), nil
	}
	ent := &tunnelPoolEntry{ready: make(chan struct{})}
	p.entries[key] = ent
	p.mu.Unlock()

	ep, stop, err := factory(ctx)
	p.mu.Lock()
	if err != nil {
		delete(p.entries, key)
		ent.err = err
		close(ent.ready)
		p.mu.Unlock()
		return TunnelEndpoint{}, nil, err
	}
	if p.closed {
		p.mu.Unlock()
		if stop != nil {
			stop()
		}
		return TunnelEndpoint{}, nil, errors.New("tunnel pool: closed")
	}
	ent.endpoint = ep
	ent.stop = stop
	ent.refcount = 1
	ent.lastUsed = time.Now()
	close(ent.ready)
	epCopy := ep
	p.mu.Unlock()
	return epCopy, p.releaseFn(key), nil
}

func (p *GlobalTunnelPool) releaseFn(key string) func() {
	return func() {
		if p == nil {
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		ent, ok := p.entries[key]
		if !ok {
			return
		}
		ent.refcount--
		ent.lastUsed = time.Now()
		if ent.refcount <= 0 {
			if ent.stop != nil {
				ent.stop()
			}
			delete(p.entries, key)
		}
	}
}

// Close stops all entries and the sweeper.
func (p *GlobalTunnelPool) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.stopCh)
	for _, ent := range p.entries {
		if ent.stop != nil {
			ent.stop()
		}
	}
	p.entries = make(map[string]*tunnelPoolEntry)
	p.mu.Unlock()
}

func (p *GlobalTunnelPool) sweepLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.sweepIdle()
		}
	}
}

func (p *GlobalTunnelPool) sweepIdle() {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, ent := range p.entries {
		if ent.refcount > 0 {
			continue
		}
		if now.Sub(ent.lastUsed) > p.ttl {
			if ent.stop != nil {
				ent.stop()
			}
			delete(p.entries, key)
		}
	}
}
