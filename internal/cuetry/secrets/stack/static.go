package stack

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
)

// StaticDataKey decrypts secure:v1:… using a fixed 32-byte key (tests; do not use in production).
type StaticDataKey struct {
	key []byte
}

// NewStatic returns a [ref.Backend] for secure: refs using key (must be [SymmetricKeyBytes] long).
func NewStatic(key []byte) (ref.Backend, error) {
	if len(key) != SymmetricKeyBytes {
		return nil, fmt.Errorf("stack.NewStatic: key must be exactly %d bytes", SymmetricKeyBytes)
	}
	cp := make([]byte, SymmetricKeyBytes)
	copy(cp, key)
	return &StaticDataKey{key: cp}, nil
}

// Name implements [ref.Backend].
func (StaticDataKey) Name() string { return "symmetric-static" }

// Handles implements [ref.Backend].
func (StaticDataKey) Handles(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), "secure:")
}

// Resolve implements [ref.Backend].
func (s *StaticDataKey) Resolve(ctx context.Context, ref string) (string, error) {
	_ = ctx
	inner := strings.TrimSpace(ref[len("secure:"):])
	return DecryptSymmetricV1(s.key, inner)
}
