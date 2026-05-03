package searchrun

import (
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
