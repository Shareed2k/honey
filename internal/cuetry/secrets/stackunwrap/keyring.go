package stackunwrap

import (
	"context"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

// Keyring loads the stack data key from the OS credential store.
// secretsprovider: keyring://service/user (encryptedkey is ignored).
type Keyring struct{}

// Name implements [DataKeyUnwrapper].
func (Keyring) Name() string { return "keyring" }

// Supports implements [DataKeyUnwrapper].
func (Keyring) Supports(providerURL string) bool {
	return strings.HasPrefix(strings.TrimSpace(providerURL), "keyring://")
}

func (Keyring) Unwrap(_ context.Context, providerURL, _ string) ([]byte, error) {
	rest := strings.TrimSpace(providerURL[len("keyring://"):])
	serviceName, user, ok := strings.Cut(rest, "/")
	serviceName, user = strings.TrimSpace(serviceName), strings.TrimSpace(user)
	if !ok || serviceName == "" || user == "" {
		return nil, fmt.Errorf("keyring stack provider must be keyring://service/user")
	}
	v, err := keyring.Get(serviceName, user)
	if err != nil {
		return nil, fmt.Errorf("keyring://%s/%s: %w", serviceName, user, err)
	}
	return DecodeKeyringMaterial(v)
}
