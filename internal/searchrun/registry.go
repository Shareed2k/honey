package searchrun

import (
	"reflect"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

// ProviderFactory defines the interface that each backend provider must implement
// to register itself with the search engine.
type ProviderFactory interface {
	// FromConfig returns a list of configured backends based on the user's YAML config.
	FromConfig(cfg *config.File, f ProviderFlags) []hosts.Backend
	// Default returns a single backend instance using CLI flags / defaults when no config is provided.
	Default(f ProviderFlags) hosts.Backend
	// BackendRows returns a summary of configured backends for listing purposes (e.g. `honey backends`).
	BackendRows(cfg *config.File) []config.BackendRow
}

var factories []ProviderFactory

// Register adds a new provider factory to the registry.
// This is typically called from an init() function within each provider package.
func Register(f ProviderFactory) {
	factories = append(factories, f)
	if r, ok := f.(BackendConfigRegistry); ok {
		registerBackendSlice(r.BackendKind(), func(cfg *config.File) reflect.Value {
			ptr := r.BackendSlicePtr(cfg)
			v := reflect.ValueOf(ptr)
			if !v.IsValid() || v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Slice {
				return reflect.Value{}
			}
			return v.Elem()
		})
	}
}

// ListSearchProviderIDs returns hosts.Backend.ID() for each registered factory's default backend,
// in registration order (matches implicit providers when config has no backend entries).
func ListSearchProviderIDs(f ProviderFlags) []string {
	ids := make([]string, 0, len(factories))
	for _, factory := range factories {
		ids = append(ids, factory.Default(f).ID())
	}
	return ids
}

// ListBackendRows queries all registered providers to build a list of configured backends.
func ListBackendRows(cfg *config.File) []config.BackendRow {
	var rows []config.BackendRow
	if cfg == nil {
		return rows
	}
	for _, factory := range factories {
		rows = append(rows, factory.BackendRows(cfg)...)
	}
	return rows
}
