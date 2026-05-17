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
		return appendExtraBackends([]ref.Backend{b}, opts), nil
	}
	p := strings.TrimSpace(opts.SecretsProvider)
	k := strings.TrimSpace(opts.EncryptedKey)
	if p == "" && k == "" {
		return appendExtraBackends([]ref.Backend{}, opts), nil
	}
	if p == "" {
		return nil, fmt.Errorf("defaults.secretsprovider is required when encryptedkey is set")
	}
	if k == "" && !providerAllowsEmptyEncryptedKey(p) {
		return nil, fmt.Errorf("defaults.encryptedkey is required for secretsprovider %q", p)
	}
	reg := defaultRegistry(opts)
	backends := make([]ref.Backend, 0, 1+len(opts.ExtraBackends))
	backends = append(backends, stack.NewDeferred(p, k, reg))
	backends = append(backends, opts.ExtraBackends...)
	return backends, nil
}

func appendExtraBackends(base []ref.Backend, opts Options) []ref.Backend {
	if len(opts.ExtraBackends) == 0 {
		return base
	}
	out := append([]ref.Backend(nil), base...)
	return append(out, opts.ExtraBackends...)
}

func providerAllowsEmptyEncryptedKey(providerURL string) bool {
	p := strings.TrimSpace(providerURL)
	return strings.HasPrefix(p, "keyring://") || strings.HasPrefix(p, "age-file://")
}
