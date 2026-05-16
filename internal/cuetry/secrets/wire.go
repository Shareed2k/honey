package secrets

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
	"github.com/shareed2k/honey/internal/cuetry/secrets/stack"
)

func defaultBackends(_ context.Context, opts Options) ([]ref.Backend, error) {
	if len(opts.SymmetricDataKey) == stack.SymmetricKeyBytes {
		b, err := stack.NewStatic(opts.SymmetricDataKey)
		if err != nil {
			return nil, err
		}
		return []ref.Backend{b}, nil
	}
	p := strings.TrimSpace(opts.SecretsProvider)
	k := strings.TrimSpace(opts.EncryptedKey)
	if p == "" && k == "" {
		return []ref.Backend{}, nil
	}
	if p == "" {
		return nil, fmt.Errorf("defaults.secretsprovider is required when encryptedkey is set")
	}
	if k == "" && !providerAllowsEmptyEncryptedKey(p) {
		return nil, fmt.Errorf("defaults.encryptedkey is required for secretsprovider %q", p)
	}
	reg := defaultRegistry(opts)
	return []ref.Backend{stack.NewDeferred(p, k, reg)}, nil
}

func providerAllowsEmptyEncryptedKey(providerURL string) bool {
	p := strings.TrimSpace(providerURL)
	return strings.HasPrefix(p, "keyring://") || strings.HasPrefix(p, "age-file://")
}
