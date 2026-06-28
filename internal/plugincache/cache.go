// Package plugincache provides a shared, reference-counted plugin manager that
// survives config reloads without disrupting in-flight requests. It is
// extracted from the webserver package so other callers (MCP server,
// scheduler, CLI) can reuse the same borrower semantics without importing
// the full web layer.
package plugincache

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/plugins"
)

// Cache holds one shared *plugins.Manager for synchronous callers, reopened
// when config changes. Callers Borrow the manager and must call the returned
// release func when done; a manager retired by Reload stays open until its
// last borrower releases it, so an in-flight request never observes a closed
// manager. Async work opens its own manager and does not use this cache.
type Cache struct {
	mu   sync.Mutex
	cfg  *config.File
	mgr  *plugins.Manager
	refs map[*plugins.Manager]int
}

// New returns a Cache initialised with cfg. The first Borrow opens the
// manager lazily.
func New(cfg *config.File) *Cache {
	return &Cache{cfg: cfg, refs: map[*plugins.Manager]int{}}
}

// Borrow returns the current shared manager and a release func the caller
// MUST invoke (via defer) when finished. The manager is guaranteed open
// until the release func runs, even across a concurrent Reload. Returns
// (nil, no-op) if the manager cannot be opened; callers must tolerate nil.
func (c *Cache) Borrow() (*plugins.Manager, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mgr == nil {
		m, err := plugins.Open(context.Background(), c.cfg)
		if err != nil {
			zap.L().Warn("plugin cache: open failed", zap.Error(err))
			return nil, func() {}
		}
		c.mgr = m
	}
	m := c.mgr
	c.refs[m]++
	return m, func() { c.release(m) }
}

func (c *Cache) release(m *plugins.Manager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refs[m]--
	if c.refs[m] <= 0 {
		delete(c.refs, m)
		if m != c.mgr {
			_ = m.Close()
		}
	}
}

// Reload swaps in a freshly opened manager built from cfg. The previous
// manager is closed immediately if it has no active borrowers; otherwise the
// last release() closes it.
func (c *Cache) Reload(cfg *config.File) {
	newMgr, err := plugins.Open(context.Background(), cfg)
	if err != nil {
		zap.L().Warn("plugin cache: reload open failed, keeping old", zap.Error(err))
		return
	}
	c.mu.Lock()
	old := c.mgr
	c.mgr = newMgr
	c.cfg = cfg
	closeOld := old != nil && c.refs[old] == 0
	c.mu.Unlock()
	if closeOld {
		_ = old.Close()
	}
}

// Close shuts down all managers held by the cache. Safe to call multiple times.
func (c *Cache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mgr != nil {
		_ = c.mgr.Close()
	}
	for m := range c.refs {
		if m != c.mgr {
			_ = m.Close()
		}
	}
	c.refs = map[*plugins.Manager]int{}
	c.mgr = nil
}
