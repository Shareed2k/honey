package searchrun

import (
	"charm.land/huh/v2"

	"github.com/shareed2k/honey/internal/config"
)

// ProviderCRUD defines the interface for creating, editing, and deleting backends interactively.
type ProviderCRUD interface {
	// ID returns the provider identifier (e.g. "gcp", "aws")
	ID() string
	// Name returns the human readable name (e.g. "GCP", "AWS")
	Name() string

	// ListOptions returns a list of huh options for the edit/delete menus
	ListOptions(cfg *config.File) []huh.Option[string]

	// Add runs the interactive prompt to add a new backend to the config
	Add(cfg *config.File) error
	// Edit runs the interactive prompt to edit an existing backend at the given index
	Edit(cfg *config.File, idx int) error
	// Delete removes the backend at the given index
	Delete(cfg *config.File, idx int) error
}

var crudHandlers []ProviderCRUD

// RegisterCRUD registers a ProviderCRUD implementation.
func RegisterCRUD(c ProviderCRUD) {
	crudHandlers = append(crudHandlers, c)
}

// GetCRUDHandlers returns all registered ProviderCRUD implementations.
func GetCRUDHandlers() []ProviderCRUD {
	return crudHandlers
}

// GetCRUDHandler returns a specific ProviderCRUD implementation by ID.
func GetCRUDHandler(id string) ProviderCRUD {
	for _, c := range crudHandlers {
		if c.ID() == id {
			return c
		}
	}
	return nil
}
