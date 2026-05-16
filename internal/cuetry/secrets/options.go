package secrets

// Options configures stack data-key unwrap and optional symmetric test keys.
type Options struct {
	// SymmetricDataKey, when exactly [stack.SymmetricKeyBytes] long, decrypts secure:v1 without KMS (tests).
	SymmetricDataKey []byte
	// SecretsProvider and EncryptedKey unwrap the stack data key (e.g. gcpkms://…, age://…).
	SecretsProvider string
	EncryptedKey    string
	// AgeIdentityFile enables age:// and age-file:// stack providers and loads age identities.
	AgeIdentityFile string
}
