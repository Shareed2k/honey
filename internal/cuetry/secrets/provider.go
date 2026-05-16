package secrets

import (
	"context"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
)

// Provider selects and wires Backends from options (same structural role as
type Provider interface {
	Backends(ctx context.Context, opts Options) ([]ref.Backend, error)
}

// DefaultProvider is the built-in wiring for honey recipe refs.
type DefaultProvider struct{}

// Backends implements [Provider].
func (DefaultProvider) Backends(ctx context.Context, opts Options) ([]ref.Backend, error) {
	return defaultBackends(ctx, opts)
}
