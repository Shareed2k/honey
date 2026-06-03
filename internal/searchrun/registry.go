package searchrun

import (
	"reflect"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

// ProviderFactory defines the interface that each backend provider must implement
// to register itself with the search engine.
type ProviderFactory interface {
	// FromConfig returns a list of configured backends based on the user's YAML config.
	// overrides is an opaque map; each factory extracts its own key and deserializes its section.
	FromConfig(cfg *config.File, overrides ProviderOverrides) []hosts.Backend
	// Default returns a single backend instance using CLI flags / defaults when no config is provided.
	Default(overrides ProviderOverrides) hosts.Backend
	// BackendRows returns a summary of configured backends for listing purposes (e.g. `honey backends`).
	BackendRows(cfg *config.File) []config.BackendRow
}

// Registry provides access to registered provider factories.
type Registry struct {
	Factories          []ProviderFactory
	backendSliceByKind map[string]func(cfg *config.File) reflect.Value
}

// NewRegistry initializes a registry with given factories and registers backend slices.
func NewRegistry(factories []ProviderFactory) *Registry {
	r := &Registry{
		Factories:          factories,
		backendSliceByKind: make(map[string]func(cfg *config.File) reflect.Value),
	}
	for _, f := range factories {
		if bc, ok := f.(BackendConfigRegistry); ok {
			r.registerBackendSlice(bc.BackendKind(), func(cfg *config.File) reflect.Value {
				ptr := bc.BackendSlicePtr(cfg)
				v := reflect.ValueOf(ptr)
				if !v.IsValid() || v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Slice {
					return reflect.Value{}
				}
				return v.Elem()
			})
		}
	}
	return r
}

// ListSearchProviderIDs returns hosts.Backend.ID() for each registered factory's default backend.
func (r *Registry) ListSearchProviderIDs(overrides ProviderOverrides) []string {
	ids := make([]string, 0, len(r.Factories))
	for _, factory := range r.Factories {
		ids = append(ids, factory.Default(overrides).ID())
	}
	return ids
}

// ListBackendRows queries all registered providers to build a list of configured backends.
func (r *Registry) ListBackendRows(cfg *config.File) []config.BackendRow {
	var rows []config.BackendRow
	if cfg == nil {
		return rows
	}
	for _, factory := range r.Factories {
		rows = append(rows, factory.BackendRows(cfg)...)
	}
	return rows
}

// FlagRegistrar is optionally implemented by provider factories that expose CLI flags.
type FlagRegistrar interface {
	RegisterFlags(cmd *cobra.Command)
}

// RegisterAllProviderFlags calls RegisterFlags on each factory that implements FlagRegistrar.
func (r *Registry) RegisterAllProviderFlags(cmd *cobra.Command) {
	for _, f := range r.Factories {
		if r, ok := f.(FlagRegistrar); ok {
			r.RegisterFlags(cmd)
		}
	}
}
