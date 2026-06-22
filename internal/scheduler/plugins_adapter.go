package scheduler

import (
	"context"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/plugins"
)

// openClosePlugins satisfies engine.PluginProvider by opening a fresh plugin
// manager per run and closing it on release. Used by async paths (scheduler)
// that own their plugin lifecycle, unlike the webserver's shared cache.
type openClosePlugins struct {
	cfg *config.File
}

// Borrow opens a fresh manager. The release func closes it. On open failure it
// returns a nil manager and a no-op release (callers tolerate a nil manager).
func (o openClosePlugins) Borrow() (*plugins.Manager, func()) {
	mgr, err := plugins.Open(context.Background(), o.cfg)
	if err != nil {
		zap.L().Warn("scheduler: plugin open failed", zap.Error(err))
		return nil, func() {}
	}
	return mgr, func() { _ = mgr.Close() }
}

// compile-time check
var _ engine.PluginProvider = openClosePlugins{}
