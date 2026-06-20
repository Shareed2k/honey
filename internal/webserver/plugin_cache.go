package webserver

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/plugins"
)

// pluginCache holds one shared *plugins.Manager for the synchronous request
// path, reopened when config changes. Callers Borrow the manager and must call
// the returned release func when done; a manager retired by Reload stays open
// until its last borrower releases it, so an in-flight request never observes a
// closed manager. Async work opens its own manager and does not use this cache.
type pluginCache struct {
	mu   sync.Mutex
	cfg  *config.File
	mgr  *plugins.Manager
	refs map[*plugins.Manager]int // live borrow count per manager
}

func newPluginCache(cfg *config.File) *pluginCache {
	return &pluginCache{cfg: cfg, refs: map[*plugins.Manager]int{}}
}

// Borrow returns the current shared manager and a release func the caller MUST
// invoke (via defer) when finished. The manager is guaranteed open until the
// release func runs, even across a concurrent Reload. Returns (nil, no-op) if
// the manager cannot be opened; callers must tolerate a nil manager.
func (p *pluginCache) Borrow() (*plugins.Manager, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mgr == nil {
		m, err := plugins.Open(context.Background(), p.cfg)
		if err != nil {
			zap.L().Warn("plugin cache: open failed", zap.Error(err))
			return nil, func() {}
		}
		p.mgr = m
	}
	m := p.mgr
	p.refs[m]++
	return m, func() { p.release(m) }
}

func (p *pluginCache) release(m *plugins.Manager) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refs[m]--
	if p.refs[m] <= 0 {
		delete(p.refs, m)
		if m != p.mgr { // retired and fully drained → safe to close
			_ = m.Close()
		}
	}
}

// Reload swaps in a freshly opened manager. The previous manager is closed now
// if it has no active borrowers, otherwise the last release() closes it.
func (p *pluginCache) Reload(cfg *config.File) {
	newMgr, err := plugins.Open(context.Background(), cfg)
	if err != nil {
		zap.L().Warn("plugin cache: reload open failed, keeping old", zap.Error(err))
		return
	}
	p.mu.Lock()
	old := p.mgr
	p.mgr = newMgr
	p.cfg = cfg
	closeOld := old != nil && p.refs[old] == 0
	p.mu.Unlock()
	if closeOld {
		_ = old.Close()
	}
}

func (p *pluginCache) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mgr != nil {
		_ = p.mgr.Close()
	}
	for m := range p.refs {
		if m != p.mgr {
			_ = m.Close()
		}
	}
	p.refs = map[*plugins.Manager]int{}
	p.mgr = nil
}
