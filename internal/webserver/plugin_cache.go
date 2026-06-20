package webserver

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/plugins"
)

// pluginCache holds one shared *plugins.Manager for the synchronous request path,
// reopened when config changes. Async work must open its own manager.
type pluginCache struct {
	mu  sync.RWMutex
	cfg *config.File
	mgr *plugins.Manager
}

func newPluginCache(cfg *config.File) *pluginCache { return &pluginCache{cfg: cfg} }

// Get returns the shared manager, opening it lazily. Callers must NOT Close it.
// Returns nil if the manager cannot be opened (callers must tolerate nil → fall
// back to plugins.Open or treat plugins as disabled).
func (p *pluginCache) Get() *plugins.Manager {
	p.mu.RLock()
	if p.mgr != nil {
		m := p.mgr
		p.mu.RUnlock()
		return m
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mgr == nil {
		m, err := plugins.Open(context.Background(), p.cfg)
		if err != nil {
			zap.L().Warn("plugin cache: open failed", zap.Error(err))
			return nil
		}
		p.mgr = m
	}
	return p.mgr
}

// Reload swaps in a freshly opened manager and closes the previous one.
func (p *pluginCache) Reload(cfg *config.File) {
	newMgr, err := plugins.Open(context.Background(), cfg)
	if err != nil {
		zap.L().Warn("plugin cache: reload open failed, keeping old", zap.Error(err))
		return
	}
	p.mu.Lock()
	old := p.mgr
	p.mgr, p.cfg = newMgr, cfg
	p.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

func (p *pluginCache) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mgr != nil {
		_ = p.mgr.Close()
		p.mgr = nil
	}
}
