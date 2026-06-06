package secrets

import "context"

// Resolver resolves a full recipe secrets ref string to plaintext (honey's execution-time contract).
type Resolver interface {
	Handles(ref string) bool
	Resolve(ctx context.Context, ref string) (string, error)
}

// NewResolver builds the default [Manager] from [Options].
func NewResolver(opts Options) (Resolver, error) {
	backends, err := defaultBackends(context.Background(), opts)
	if err != nil {
		return nil, err
	}
	return NewManager(backends...), nil
}
