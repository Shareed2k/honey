// Package ref holds shared contracts for recipe secret backends (similar in role to
// how honey centralizes crypto provider contracts).
package ref

import "context"

// Backend resolves one family of recipe secret ref prefixes. Implementations live
// under env/, service/, cloud/, passphrase/, k8s/ — mirroring honey layout.
type Backend interface {
	// Name identifies this backend for errors and debugging.
	Name() string
	// Handles reports whether this backend should resolve ref (typically prefix match).
	Handles(ref string) bool
	Resolve(ctx context.Context, ref string) (string, error)
}
