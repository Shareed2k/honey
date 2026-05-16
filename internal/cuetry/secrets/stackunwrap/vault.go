package stackunwrap

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	vaultapi "github.com/hashicorp/vault/api"
)

// VaultTransit decrypts the stack data key via Vault Transit.
// secretsprovider: vault-transit://mount/keyName
// encryptedkey: transit ciphertext (vault:v1:… or raw).
type VaultTransit struct{}

// Name implements [DataKeyUnwrapper].
func (VaultTransit) Name() string { return "vault-transit" }

// Supports implements [DataKeyUnwrapper].
func (VaultTransit) Supports(providerURL string) bool {
	return strings.HasPrefix(strings.TrimSpace(providerURL), "vault-transit://")
}

func (VaultTransit) Unwrap(_ context.Context, providerURL, encryptedKey string) ([]byte, error) {
	rest := strings.TrimSpace(providerURL[len("vault-transit://"):])
	mount, keyName, ok := strings.Cut(rest, "/")
	mount, keyName = strings.TrimSpace(mount), strings.TrimSpace(keyName)
	if !ok || mount == "" || keyName == "" {
		return nil, fmt.Errorf("vault-transit stack provider must be vault-transit://mount/keyName")
	}
	ct := strings.TrimSpace(encryptedKey)
	if ct == "" {
		return nil, fmt.Errorf("vault-transit: empty encryptedkey ciphertext")
	}
	cfg := vaultapi.DefaultConfig()
	client, err := vaultapi.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("%s/decrypt/%s", mount, keyName)
	sec, err := client.Logical().Write(path, map[string]any{"ciphertext": ct})
	if err != nil {
		return nil, fmt.Errorf("vault transit decrypt: %w", err)
	}
	if sec == nil || sec.Data == nil {
		return nil, fmt.Errorf("vault transit: empty response")
	}
	plain, _ := sec.Data["plaintext"].(string)
	if plain == "" {
		return nil, fmt.Errorf("vault transit: missing plaintext")
	}
	out, err := base64.StdEncoding.DecodeString(plain)
	if err != nil {
		return nil, fmt.Errorf("vault transit plaintext base64: %w", err)
	}
	return out, nil
}
