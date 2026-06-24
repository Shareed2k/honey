package cuetry

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry/secrets"
	"github.com/shareed2k/honey/internal/plugins"
)

// SecretResolver resolves recipe secret refs (secure:v1:…) to plaintext at execute time.
type SecretResolver interface {
	Handles(ref string) bool
	Resolve(ctx context.Context, ref string) (string, error)
}

// SecretResolverOptions configures the default secret resolver.
type SecretResolverOptions struct {
	SymmetricDataKey []byte
	SecretsProvider  string
	EncryptedKey     string
	AgeIdentityFile  string
}

// SecretResolverOptionsFromHoney maps honey YAML defaults into resolver options.
func SecretResolverOptionsFromHoney(cfg *config.File) SecretResolverOptions {
	o := SecretResolverOptions{}
	if cfg != nil {
		o.SecretsProvider = strings.TrimSpace(cfg.Defaults.SecretsProvider)
		o.EncryptedKey = strings.TrimSpace(cfg.Defaults.EncryptedKey)
	}
	if p := strings.TrimSpace(os.Getenv("HONEY_AGE_IDENTITY_FILE")); p != "" {
		o.AgeIdentityFile = p
	}
	return o
}

// NewSecretResolver builds the default resolver for recipe execution.
func NewSecretResolver(opts SecretResolverOptions) (SecretResolver, error) {
	return NewSecretResolverWithPlugins(opts, nil)
}

// NewSecretResolverWithPlugins appends WASM plugin secret backends when mgr is non-nil.
func NewSecretResolverWithPlugins(opts SecretResolverOptions, mgr *plugins.Manager) (SecretResolver, error) {
	secOpts := secrets.Options{
		SymmetricDataKey: opts.SymmetricDataKey,
		SecretsProvider:  opts.SecretsProvider,
		EncryptedKey:     opts.EncryptedKey,
		AgeIdentityFile:  opts.AgeIdentityFile,
	}
	if mgr != nil {
		secOpts.ExtraBackends = mgr.SecretRefBackends()
	}
	return secrets.NewResolver(secOpts)
}

// StaticSecretResolver provides a static map of secrets for testing and simple use cases.
type StaticSecretResolver map[string]string

// Handles returns true if the reference exists in the static map.
func (m StaticSecretResolver) Handles(ref string) bool {
	_, ok := m[ref]
	return ok
}

// Resolve returns the value from the static map if it exists.
func (m StaticSecretResolver) Resolve(_ context.Context, ref string) (string, error) {
	if val, ok := m[ref]; ok {
		return val, nil
	}
	return "", fmt.Errorf("secret not found")
}
