package hosts

import "context"

// Backend is implemented by each cloud integration.
type Backend interface {
	ID() string
	Search(ctx context.Context, q Query) ([]Record, error)
}
