package plugins

import (
	"context"

	"github.com/shareed2k/honey/internal/config"
)

// Open loads plugins from honey config (returns non-nil manager even when disabled).
func Open(ctx context.Context, cfg *config.File) (*Manager, error) {
	return NewManager(ctx, PluginsFromConfig(cfg))
}
