package plugins

import (
	"context"
	"fmt"
	"strings"
)

const secureV1Prefix = "secure:v1:"

// SecretResolveFunc resolves a secure:v1 ref to plaintext (operator-side only).
type SecretResolveFunc func(ctx context.Context, ref string) (string, error)

// ResolvePostgresDSN resolves config.dsn_secret from a secrets map key or direct secure:v1 ref.
func ResolvePostgresDSN(ctx context.Context, h *HostRunContext, ref string) (string, error) {
	if h == nil {
		return "", fmt.Errorf("host run context not available")
	}
	if h.SecretsDry || !h.Execute {
		return "", fmt.Errorf("postgres dsn resolve skipped on dry-run")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("dsn_secret is required")
	}
	secureRef := ref
	if !strings.HasPrefix(ref, secureV1Prefix) {
		v, ok := h.RecipeSecrets[ref]
		if !ok {
			return "", fmt.Errorf("unknown secrets key %q", ref)
		}
		v = strings.TrimSpace(v)
		if !strings.HasPrefix(v, secureV1Prefix) {
			return "", fmt.Errorf("secrets key %q must reference secure:v1", ref)
		}
		secureRef = v
	}
	if h.ResolveSecret == nil {
		return "", fmt.Errorf("secret resolver not configured")
	}
	return h.ResolveSecret(ctx, secureRef)
}
