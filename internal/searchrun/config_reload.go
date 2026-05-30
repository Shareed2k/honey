package searchrun

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
)

// ConfigReloader is optionally implemented by factories that hold provider-specific runtime state.
// ReconfigureFromConfig is called whenever the honey config is (re)loaded.
type ConfigReloader interface {
	ReconfigureFromConfig(cfg *config.File)
}

func init() {
	hostexec.SetConfigReloader(func(cfg *config.File) {
		for _, f := range factories {
			if r, ok := f.(ConfigReloader); ok {
				r.ReconfigureFromConfig(cfg)
			}
		}
	})
}
