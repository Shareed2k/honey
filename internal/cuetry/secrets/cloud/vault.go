// Package cloud resolves cloud and enterprise secret refs (Vault, AWS), analogous to
package cloud

import (
	"context"
	"fmt"
	"strings"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
)

// VaultBackend implements [ref.Backend] for vault:path#field.
type VaultBackend struct{}

// NewVault returns a Vault backend.
func NewVault() ref.Backend { return VaultBackend{} }

// Name implements [ref.Backend].
func (VaultBackend) Name() string { return "vault" }

// Handles implements [ref.Backend].
func (VaultBackend) Handles(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), "vault:")
}

// Resolve implements [ref.Backend].
func (VaultBackend) Resolve(_ context.Context, ref string) (string, error) {
	return resolveVault(ref[len("vault:"):])
}

func resolveVault(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	field := ""
	if i := strings.LastIndex(ref, "#"); i >= 0 {
		field = strings.TrimSpace(ref[i+1:])
		ref = strings.TrimSpace(ref[:i])
	}
	if ref == "" {
		return "", fmt.Errorf("vault: missing path")
	}
	cfg := vaultapi.DefaultConfig()
	client, err := vaultapi.NewClient(cfg)
	if err != nil {
		return "", err
	}
	sec, err := client.Logical().Read(ref)
	if err != nil {
		return "", fmt.Errorf("vault read %q: %w", ref, err)
	}
	if sec == nil || sec.Data == nil {
		return "", fmt.Errorf("vault: no data at %q", ref)
	}
	if inner, ok := sec.Data["data"].(map[string]any); ok {
		if field == "" {
			return "", fmt.Errorf("vault: field name required as path#field for KV v2 style response at %q", ref)
		}
		v, ok := inner[field]
		if !ok {
			return "", fmt.Errorf("vault: field %q not found under %q", field, ref)
		}
		s, _ := v.(string)
		if s == "" {
			return "", fmt.Errorf("vault: field %q is empty or not a string", field)
		}
		return s, nil
	}
	if field != "" {
		if v, ok := sec.Data[field]; ok {
			s, _ := v.(string)
			if s != "" {
				return s, nil
			}
		}
	}
	if field == "" {
		if v, ok := sec.Data["value"].(string); ok && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("vault: could not resolve value from %q (use path#field for keyed secrets)", ref)
}
