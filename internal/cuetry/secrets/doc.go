// Package secrets resolves CUE recipe secret refs.
//
// Recipe values must be symmetric: secure:v1:<nonce-b64>:<ciphertext-b64>.
// The stack data key comes from honey defaults.secretsprovider + defaults.encryptedkey
// (or SymmetricDataKey in tests), unwrapped via [stackunwrap.DataKeyUnwrapper] providers:
//
//   - gcpkms://projects/…/locations/…/keyRings/…/cryptoKeys/… — encryptedkey is KMS ciphertext (base64)
//   - awskms:// — encryptedkey is base64 KMS ciphertext blob
//   - vault-transit://mount/keyName — encryptedkey is transit ciphertext
//   - k8s://namespace/secretName — encryptedkey is Secret data key name (32 raw bytes or base64)
//   - keyring://service/user — OS keyring holds the data key (base64 or raw)
//   - age:// — encryptedkey is armored age ciphertext (requires AgeIdentityFile)
//   - age-file://path — ciphertext file on disk (requires AgeIdentityFile)
//
// Authoring: honey secrets keyring-init (local OS keyring), honey secrets seal, and
// honey secrets unseal for recipe secure:v1 values with the same stack key as runtime resolution.
package secrets
