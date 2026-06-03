package searchrun

import (
	"github.com/shareed2k/honey/internal/config"
)

// ConfigReloader is optionally implemented by factories that hold provider-specific runtime state.
// ReconfigureFromConfig is called whenever the honey config is (re)loaded.
type ConfigReloader interface {
	ReconfigureFromConfig(cfg *config.File)
}

// ReconfigureFromConfig propagates config to all registered provider factories.
func (r *Registry) ReconfigureFromConfig(cfg *config.File) {
	for _, f := range r.Factories {
		if reloader, ok := f.(ConfigReloader); ok {
			reloader.ReconfigureFromConfig(cfg)
		}
	}
}
