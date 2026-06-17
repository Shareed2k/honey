package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/hosts"
	"go.uber.org/zap"
)

// RecipeTunnelCoordinator tracks tunnel endpoints for one cue-exec run and releases pool refs on Close.
// RecipeTunnelCoordinator ...
type RecipeTunnelCoordinator struct {
	pool     *GlobalTunnelPool
	mu       sync.Mutex
	releases []func()
	lookup   map[string]TunnelEndpoint
	closed   bool
}

// NewRecipeTunnelCoordinator creates a coordinator backed by the process-wide pool.
// NewRecipeTunnelCoordinator ...
func NewRecipeTunnelCoordinator(pool *GlobalTunnelPool) *RecipeTunnelCoordinator {
	if pool == nil {
		pool = DefaultGlobalTunnelPool()
	}
	return &RecipeTunnelCoordinator{
		pool:   pool,
		lookup: make(map[string]TunnelEndpoint),
	}
}

// Acquire obtains or creates a tunnel from the global pool.
func (c *RecipeTunnelCoordinator) Acquire(ctx context.Context, key string, factory func(context.Context) (TunnelEndpoint, func(), error)) (TunnelEndpoint, func(), error) {
	if c == nil || c.pool == nil {
		return TunnelEndpoint{}, nil, fmt.Errorf("tunnel coordinator: nil")
	}
	return c.pool.Acquire(ctx, key, factory)
}

// Register stores an endpoint for tunnel_step lookup and holds the pool release until Close.
func (c *RecipeTunnelCoordinator) Register(stepID, user string, r hosts.Record, ep TunnelEndpoint, release func()) {
	if c == nil {
		if release != nil {
			release()
		}
		return
	}
	key := tunnelLookupKey(stepID, user, r)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		if release != nil {
			release()
		}
		return
	}
	c.lookup[key] = ep
	if release != nil {
		c.releases = append(c.releases, release)
	}
	zap.L().Debug("recipe tunnel register",
		zap.String("step_id", stepID),
		zap.String("host_name", r.Name),
		zap.String("endpoint_host", ep.Host),
		zap.Int("endpoint_port", ep.Port),
		zap.String("mode", ep.Mode),
	)
}

// Lookup returns the endpoint for a tunnel step id and host.
func (c *RecipeTunnelCoordinator) Lookup(stepID, user string, r hosts.Record) (TunnelEndpoint, bool) {
	if c == nil {
		return TunnelEndpoint{}, false
	}
	key := tunnelLookupKey(stepID, user, r)
	c.mu.Lock()
	defer c.mu.Unlock()
	ep, ok := c.lookup[key]
	return ep, ok
}

// Close releases all pool references held by this run.
func (c *RecipeTunnelCoordinator) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for _, rel := range c.releases {
		if rel != nil {
			rel()
		}
	}
	c.releases = nil
	c.lookup = make(map[string]TunnelEndpoint)
}

func tunnelLookupKey(stepID, user string, r hosts.Record) string {
	return stepID + "\x00" + SSHClientCacheKey(user, r)
}

// TunnelLookupKeyForShare returns a stable global pool key from recipe tunnel config.
// TunnelLookupKeyForShare ...
func TunnelLookupKeyForShare(shareKey, derivedKey string) string {
	if s := shareKey; s != "" {
		return "share:" + s
	}
	return "derived:" + derivedKey
}

// TunnelDerivedKey ...
func TunnelDerivedKey(mode, provider, hostKey, spec string) string {
	return fmt.Sprintf("%s|%s|%s|%s", mode, provider, hostKey, spec)
}

// LookupEndpoint implements plugins.TunnelCoordinator for postgres DSN rewrite.
func (c *RecipeTunnelCoordinator) LookupEndpoint(stepID, user string, r hosts.Record) (string, int, bool) {
	ep, ok := c.Lookup(stepID, user, r)
	if !ok || ep.Mode == "tun" || ep.Port <= 0 {
		zap.L().Debug("recipe tunnel lookup miss",
			zap.String("step_id", stepID),
			zap.String("host_name", r.Name),
			zap.Bool("found", ok),
		)
		return "", 0, false
	}
	host := strings.TrimSpace(ep.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	zap.L().Debug("recipe tunnel lookup hit",
		zap.String("step_id", stepID),
		zap.String("host_name", r.Name),
		zap.String("endpoint_host", host),
		zap.Int("endpoint_port", ep.Port),
	)
	return host, ep.Port, true
}
