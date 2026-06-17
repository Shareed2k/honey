package secrets

import (
	"context"
	"filippo.io/age"
)

// Options configures stack data-key unwrap and optional symmetric test keys.
type Options struct {
	// SymmetricDataKey, when exactly 32 bytes long, decrypts secure:v1 without KMS (tests).
	SymmetricDataKey []byte
	// SecretsProvider and EncryptedKey unwrap the stack data key (e.g. gcpkms://…, age://…).
	SecretsProvider string
	EncryptedKey    string
	// AgeIdentityFile enables age:// and age-file:// stack providers and loads age identities.
	AgeIdentityFile string
	// AgeIdentities are parsed age identities.
	AgeIdentities []age.Identity
	// RecipeDir provides the base directory for age-file: resolution.
	RecipeDir func(context.Context) string
	// ExtraBackends append plugin or test secret backends after built-in wiring.
	ExtraBackends []ExtraBackend
}
