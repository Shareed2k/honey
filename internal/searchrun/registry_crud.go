package searchrun

import (
	"sync"

	"charm.land/huh/v2"
)

// ProviderCRUD defines the interface for creating, editing, and deleting backends interactively.
type ProviderCRUD interface {
	// ID returns the provider identifier (e.g. "gcp", "aws")
	ID() string
	// Name returns the human readable name (e.g. "GCP", "AWS")
	Name() string

	// ListOptions returns a list of huh options for the edit/delete menus
	ListOptions() []huh.Option[string]

	// Add runs the interactive prompt to add a new backend to the config
	Add() error
	// Edit runs the interactive prompt to edit an existing backend at the given index
	Edit(idx int) error
	// Delete removes the backend at the given index
	Delete(idx int) error
}

var (
	crudMu       sync.RWMutex
	crudHandlers []ProviderCRUD
)

// RegisterCRUD registers a ProviderCRUD implementation.
func RegisterCRUD(c ProviderCRUD) {
	crudMu.Lock()
	defer crudMu.Unlock()
	crudHandlers = append(crudHandlers, c)
}

// CRUDHandlers returns a snapshot of all registered ProviderCRUD implementations.
func CRUDHandlers() []ProviderCRUD {
	crudMu.RLock()
	defer crudMu.RUnlock()
	out := make([]ProviderCRUD, len(crudHandlers))
	copy(out, crudHandlers)
	return out
}

// CRUDHandler returns a specific ProviderCRUD implementation by ID.
func CRUDHandler(id string) ProviderCRUD {
	crudMu.RLock()
	defer crudMu.RUnlock()
	for _, c := range crudHandlers {
		if c.ID() == id {
			return c
		}
	}
	return nil
}
