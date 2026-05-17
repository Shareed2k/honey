package ui

import (
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/shareed2k/honey/internal/hosts"
	"go.uber.org/zap"
)

// ClientCache maintains a pool of open HostClient connections for reuse across steps.
type ClientCache struct {
	mu      sync.Mutex
	clients map[string]HostClient

	hits         int64
	misses       int64
	raceHits     int64
	dialAttempts int64
	dialErrors   int64
}

// NewClientCache creates a new initialized ClientCache.
func NewClientCache() *ClientCache {
	return &ClientCache{
		clients: make(map[string]HostClient),
	}
}

// SSHClientCacheKey is the stable cache key for a pooled SSH client for (user, record).
func SSHClientCacheKey(user string, r hosts.Record) string {
	port := "-"
	if p, ok := hosts.MetaSSHPort(&r); ok {
		port = strconv.Itoa(p)
	}
	identity := "-"
	if id, ok := hosts.MetaSSHIdentityFile(&r); ok {
		identity = id
	}
	return r.Provider + "\x00" + r.PrimaryIP + "\x00" + r.Name + "\x00" + user + "\x00" + port + "\x00" + identity
}

// GetOrDial returns an existing connection or dials a new one and stores it.
func (c *ClientCache) GetOrDial(user string, r hosts.Record) (HostClient, error) {
	if c == nil {
		return GetExecutor(r).Dial(user, r)
	}

	key := SSHClientCacheKey(user, r)

	c.mu.Lock()
	client, exists := c.clients[key]
	c.mu.Unlock()

	if exists {
		atomic.AddInt64(&c.hits, 1)
		zap.L().Debug("ssh client cache hit",
			zap.String("provider", r.Provider),
			zap.String("host_name", r.Name),
			zap.String("host_ip", r.PrimaryIP),
			zap.String("user", user),
		)
		return client, nil
	}
	atomic.AddInt64(&c.misses, 1)
	atomic.AddInt64(&c.dialAttempts, 1)
	zap.L().Debug("ssh client cache miss (dialing new client)",
		zap.String("provider", r.Provider),
		zap.String("host_name", r.Name),
		zap.String("host_ip", r.PrimaryIP),
		zap.String("user", user),
	)

	// Dial outside the lock so parallel connections don't block each other
	client, err := GetExecutor(r).Dial(user, r)
	if err != nil {
		atomic.AddInt64(&c.dialErrors, 1)
		return nil, err
	}

	c.mu.Lock()
	// Check again in case another goroutine dialed the same host concurrently
	if existing, exists := c.clients[key]; exists {
		c.mu.Unlock()
		_ = client.Close()
		atomic.AddInt64(&c.raceHits, 1)
		zap.L().Debug("ssh client cache race-hit (reusing existing client)",
			zap.String("provider", r.Provider),
			zap.String("host_name", r.Name),
			zap.String("host_ip", r.PrimaryIP),
			zap.String("user", user),
		)
		return existing, nil
	}
	c.clients[key] = client
	c.mu.Unlock()
	zap.L().Debug("ssh client cached new connection",
		zap.String("provider", r.Provider),
		zap.String("host_name", r.Name),
		zap.String("host_ip", r.PrimaryIP),
		zap.String("user", user),
	)

	return client, nil
}

// getByKey returns the cached client for an SSHClientCacheKey, or nil if absent.
// Used by the fallback-path runner to reuse already-pooled SSH connections.
func (c *ClientCache) getByKey(key string) HostClient {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clients[key]
}

func (c *ClientCache) debugKeys() []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var keys []string
	for k := range c.clients {
		keys = append(keys, k)
	}
	return keys
}

// Evict removes the cached client for this host (if any) and closes it so the
// next GetOrDial establishes a fresh connection.
func (c *ClientCache) Evict(user string, r hosts.Record) {
	if c == nil {
		return
	}
	key := SSHClientCacheKey(user, r)
	c.mu.Lock()
	client, ok := c.clients[key]
	if ok {
		delete(c.clients, key)
	}
	c.mu.Unlock()
	if !ok || client == nil {
		return
	}
	_ = client.Close()
	zap.L().Debug("ssh client cache evicted",
		zap.String("provider", r.Provider),
		zap.String("host_name", r.Name),
		zap.String("host_ip", r.PrimaryIP),
		zap.String("user", user),
	)
}

// CloseAll closes all cached connections and clears the cache.
func (c *ClientCache) CloseAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cachedConnections := len(c.clients)
	for _, client := range c.clients {
		_ = client.Close()
	}
	c.clients = make(map[string]HostClient)
	zap.L().Debug("ssh client cache summary",
		zap.Int64("cache_hits", atomic.LoadInt64(&c.hits)),
		zap.Int64("cache_misses", atomic.LoadInt64(&c.misses)),
		zap.Int64("cache_race_hits", atomic.LoadInt64(&c.raceHits)),
		zap.Int64("dial_attempts", atomic.LoadInt64(&c.dialAttempts)),
		zap.Int64("dial_errors", atomic.LoadInt64(&c.dialErrors)),
		zap.Int("closed_cached_connections", cachedConnections),
	)
}
