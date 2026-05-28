// Package appsecret handles decryption of secure:v1 app upstream DSNs.
package appsecret

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/cuetry/secrets"
)

const prefix = "secure:v1:"

// IsEncryptedUpstream reports whether v is a secure:v1 encrypted reference.
func IsEncryptedUpstream(v string) bool {
	return strings.HasPrefix(strings.TrimSpace(v), prefix)
}

// ResolveUpstream decrypts secure:v1:<ciphertext> values to plaintext DSN.
// Non-secure values are returned unchanged.
func ResolveUpstream(ctx context.Context, cfg *config.File, upstream string) (string, error) {
	upstream = strings.TrimSpace(upstream)
	if !IsEncryptedUpstream(upstream) {
		return upstream, nil
	}
	ref := upstream
	o := cuetry.SecretResolverOptionsFromHoney(cfg)
	plain, err := secrets.Unseal(ctx, secrets.Options{
		SymmetricDataKey: o.SymmetricDataKey,
		SecretsProvider:  o.SecretsProvider,
		EncryptedKey:     o.EncryptedKey,
		AgeIdentityFile:  o.AgeIdentityFile,
	}, ref)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt app upstream")
	}
	return strings.TrimSpace(plain), nil
}
