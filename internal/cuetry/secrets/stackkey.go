package secrets

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry/secrets/stack"
)

// ResolveStackDataKey returns the 32-byte AES stack key from opts (static test key or registry unwrap).
func ResolveStackDataKey(ctx context.Context, opts Options) ([]byte, error) {
	if len(opts.SymmetricDataKey) == stack.SymmetricKeyBytes {
		out := make([]byte, stack.SymmetricKeyBytes)
		copy(out, opts.SymmetricDataKey)
		return out, nil
	}
	p := strings.TrimSpace(opts.SecretsProvider)
	k := strings.TrimSpace(opts.EncryptedKey)
	if p == "" {
		return nil, fmt.Errorf("stack data key: set defaults.secretsprovider and defaults.encryptedkey in honey config, or pass --data-key-file / --data-key-hex")
	}
	if k == "" && !providerAllowsEmptyEncryptedKey(p) {
		return nil, fmt.Errorf("stack data key: defaults.encryptedkey is required for secretsprovider %q", p)
	}
	raw, err := defaultRegistry(opts).Unwrap(ctx, p, k)
	if err != nil {
		return nil, err
	}
	if len(raw) != stack.SymmetricKeyBytes {
		return nil, fmt.Errorf("stack data key: expected %d bytes, got %d", stack.SymmetricKeyBytes, len(raw))
	}
	out := make([]byte, stack.SymmetricKeyBytes)
	copy(out, raw)
	return out, nil
}
