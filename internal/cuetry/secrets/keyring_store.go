package secrets

import (
	"crypto/rand"
	"fmt"
	"io"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/shareed2k/honey/internal/cuetry/secrets/stack"
	"github.com/shareed2k/honey/internal/cuetry/secrets/stackunwrap"
)

// KeyringProviderURL returns the secretsprovider value for keyring://service/user.
func KeyringProviderURL(service, user string) string {
	service = strings.TrimSpace(service)
	user = strings.TrimSpace(user)
	return "keyring://" + service + "/" + user
}

// FormatKeyringStackKeyValue encodes a 32-byte stack key for OS keyring storage.
func FormatKeyringStackKeyValue(key []byte) (string, error) {
	return stackunwrap.EncodeKeyringMaterial(key)
}

// KeyringEntryExists reports whether keyring.Get succeeds for service/user.
func KeyringEntryExists(service, user string) (bool, error) {
	_, err := keyring.Get(strings.TrimSpace(service), strings.TrimSpace(user))
	if err == keyring.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// StoreStackDataKeyInKeyring writes key to the OS credential store (base64-encoded).
func StoreStackDataKeyInKeyring(service, user string, key []byte) error {
	service = strings.TrimSpace(service)
	user = strings.TrimSpace(user)
	if service == "" || user == "" {
		return fmt.Errorf("keyring: service and user are required")
	}
	val, err := FormatKeyringStackKeyValue(key)
	if err != nil {
		return err
	}
	if err := keyring.Set(service, user, val); err != nil {
		return fmt.Errorf("keyring set %s/%s: %w", service, user, err)
	}
	return nil
}

// GenerateStackDataKey returns a random 32-byte AES stack key.
func GenerateStackDataKey() ([]byte, error) {
	key := make([]byte, stack.SymmetricKeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate stack data key: %w", err)
	}
	return key, nil
}

// KeyringConfigSnippet returns YAML defaults for a keyring stack provider.
func KeyringConfigSnippet(providerURL string) string {
	return fmt.Sprintf(`defaults:
  secretsprovider: %s
  encryptedkey: ""
`, providerURL)
}
