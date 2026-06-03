package searchrun

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

// BuildProviders returns backends from the config file when it defines at least
// one backend entry; otherwise it requests the default backend from each registered provider.
func (r *Registry) BuildProviders(cfg *config.File, overrides ProviderOverrides) []hosts.Backend {
	if overrides == nil {
		overrides = ProviderOverrides{}
	}
	out := make([]hosts.Backend, 0, len(r.Factories))

	if cfg != nil && cfg.HasAnyBackend() {
		for _, factory := range r.Factories {
			out = append(out, factory.FromConfig(cfg, overrides)...)
		}
		return out
	}

	for _, factory := range r.Factories {
		out = append(out, factory.Default(overrides))
	}
	return out
}
