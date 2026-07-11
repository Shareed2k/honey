// Package backendruntime provides a shared, thread-safe named-lookup
// registry for provider runtime configs (API credentials, exec mode).
//
// truenasprovider, proxmoxprovider, and dockerprovider each hold connection
// details for their backends.<kind>[] config entries, keyed by name, so
// exec-time code (SSH dial, tunnels, VNC, KV bridge) can resolve a
// hosts.Record's tagged backend name back to live credentials. Before this
// package existed, all three providers hand-rolled an identical
// mutex+slice+linear-scan registry; Registry concentrates that one
// implementation instead of leaving it duplicated three times.
package backendruntime

import (
	"strings"
	"sync"
)

// Registry is a thread-safe, name-indexed store for a provider's runtime
// configs, rebuilt wholesale on every config reload via Reconfigure.
type Registry[T any] struct {
	mu     sync.RWMutex
	items  []T
	nameOf func(T) string
}

// New creates a Registry that extracts each item's name via nameOf.
func New[T any](nameOf func(T) string) *Registry[T] {
	return &Registry[T]{nameOf: nameOf}
}

// Reconfigure atomically replaces the registry's contents.
func (r *Registry[T]) Reconfigure(items []T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = items
}

// ByName returns the entry matching name (empty name matches the first entry).
func (r *Registry[T]) ByName(name string) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name = strings.TrimSpace(name)
	if len(r.items) == 0 {
		var zero T
		return zero, false
	}
	if name == "" {
		return r.items[0], true
	}
	for _, it := range r.items {
		if r.nameOf(it) == name {
			return it, true
		}
	}
	var zero T
	return zero, false
}
