// Honey unwraps defaults.secretsprovider + defaults.encryptedkey to the 32-byte stack data key, then
// decrypts each secure:v1:… ref. Supported providers include gcpkms://…, awskms://, vault-transit://…,
// k8s://namespace/secret, keyring://service/user, age://, age-file://path (see secrets package doc).
//
// --- Honey YAML (excerpt) ---
// defaults:
//   secretsprovider: gcpkms://projects/PROJECT/locations/REGION/keyRings/RING/cryptoKeys/KEY
//   encryptedkey: "<provider-specific ciphertext or secret data key name>"
//
// --- Ref shape ---
// secure:v1:<base64-12-byte-nonce>:<base64-aesgcm-ciphertext>
//
// --- Try dry-run (no decrypt) ---
//   honey cue-exec examples/recipe/with_secrets_stores.cue my-filter
//
// --- Execute ---
// Requires valid honey config + ciphertexts produced with your stack key (replace placeholders below).
recipe: {
	name: "with-secrets-stores"
	defaults: {
		secrets: {DEMO_FROM_REF: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"}
	}
	steps: [
		{
			host:    "*"
			command: "printenv DEMO_FROM_REF | wc -c"
		},
	]
}
