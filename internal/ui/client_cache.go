package ui

import (
	"honey/internal/hosts"
	"sync"
)

// ClientCache maintains a pool of open HostClient connections for reuse across steps.
type ClientCache struct {
	mu      sync.Mutex
	clients map[string]HostClient
}

func NewClientCache() *ClientCache {
	return &ClientCache{
		clients: make(map[string]HostClient),
	}
}

// GetOrDial returns an existing connection or dials a new one and stores it.
func (c *ClientCache) GetOrDial(user string, r hosts.Record) (HostClient, error) {
	if c == nil {
		return GetExecutor(r).Dial(user, r)
	}

	key := r.Provider + "\x00" + r.PrimaryIP + "\x00" + r.Name + "\x00" + user

	c.mu.Lock()
	client, exists := c.clients[key]
	c.mu.Unlock()

	if exists {
		return client, nil
	}

	// Dial outside the lock so parallel connections don't block each other
	client, err := GetExecutor(r).Dial(user, r)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	// Check again in case another goroutine dialed the same host concurrently
	if existing, exists := c.clients[key]; exists {
		c.mu.Unlock()
		_ = client.Close()
		return existing, nil
	}
	c.clients[key] = client
	c.mu.Unlock()

	return client, nil
}

// CloseAll closes all cached connections and clears the cache.
func (c *ClientCache) CloseAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, client := range c.clients {
		_ = client.Close()
	}
	c.clients = make(map[string]HostClient)
}
