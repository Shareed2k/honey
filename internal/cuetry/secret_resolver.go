package cuetry

import (
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry/secrets"
)

// SecretResolver resolves recipe secret refs (secure:v1:…) to plaintext at execute time.
type SecretResolver = secrets.Resolver

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
	return secrets.NewResolver(secrets.Options{
		SymmetricDataKey: opts.SymmetricDataKey,
		SecretsProvider:  opts.SecretsProvider,
		EncryptedKey:     opts.EncryptedKey,
		AgeIdentityFile:  opts.AgeIdentityFile,
	})
}
