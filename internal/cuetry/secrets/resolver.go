package secrets

import "context"

// Resolver resolves a full recipe secrets ref string to plaintext (honey's execution-time contract).
type Resolver interface {
	Resolve(ctx context.Context, ref string) (string, error)
}

// NewResolver builds the default [Manager] using [DefaultProvider] and [Options].
func NewResolver(opts Options) (Resolver, error) {
	p := DefaultProvider{}
	backends, err := p.Backends(context.Background(), opts)
	if err != nil {
		return nil, err
	}
	return NewManager(backends...), nil
}
