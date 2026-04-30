// Package hosts defines the host search record model and pluggable cloud backends.
package hosts

import "context"

// Backend is implemented by each cloud integration.
type Backend interface {
	ID() string
	// BackendName is an optional config label (YAML backends.*.name) used with --backends.
	// Empty for unnamed backends (e.g. default flag-only quartet).
	BackendName() string
	// CacheIdentity distinguishes multiple instances with the same ID() (e.g. two GCP projects).
	// May be empty when a single implicit backend is used.
	CacheIdentity() string
	Search(ctx context.Context, q Query) ([]Record, error)
}
