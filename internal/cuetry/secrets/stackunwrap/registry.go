package stackunwrap

import (
	"context"
	"fmt"
	"strings"
)

// Registry dispatches stack data-key unwrap to registered providers.
type Registry struct {
	providers []DataKeyUnwrapper
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register appends a provider (first match wins).
func (r *Registry) Register(p DataKeyUnwrapper) {
	if p == nil {
		return
	}
	r.providers = append(r.providers, p)
}

// Unwrap selects a provider and returns the raw data key bytes.
func (r *Registry) Unwrap(ctx context.Context, providerURL, encryptedKey string) ([]byte, error) {
	providerURL = strings.TrimSpace(providerURL)
	if providerURL == "" {
		return nil, fmt.Errorf("empty secretsprovider")
	}
	for _, p := range r.providers {
		if p.Supports(providerURL) {
			return p.Unwrap(ctx, providerURL, encryptedKey)
		}
	}
	names := make([]string, 0, len(r.providers))
	for _, p := range r.providers {
		names = append(names, p.Name())
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("unsupported secretsprovider %q (no providers registered)", providerURL)
	}
	return nil, fmt.Errorf("unsupported secretsprovider %q (registered: %s)", providerURL, strings.Join(names, ", "))
}
