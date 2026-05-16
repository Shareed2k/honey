package secrets

import (
	"context"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry/secrets/stack"
)

// Seal encrypts plaintext with the stack data key and returns a full secure:v1 ref.
func Seal(ctx context.Context, opts Options, plaintext string) (string, error) {
	key, err := ResolveStackDataKey(ctx, opts)
	if err != nil {
		return "", err
	}
	return stack.FormatSecureRef(key, plaintext)
}

// Unseal decrypts a secure:v1 ref to plaintext using the stack data key.
func Unseal(ctx context.Context, opts Options, ref string) (string, error) {
	if err := stack.ValidateSecureRef(ref); err != nil {
		return "", err
	}
	key, err := ResolveStackDataKey(ctx, opts)
	if err != nil {
		return "", err
	}
	inner := strings.TrimSpace(ref[len("secure:"):])
	return stack.DecryptSymmetricV1(key, inner)
}
