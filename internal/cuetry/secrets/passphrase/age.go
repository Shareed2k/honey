// Package passphrase resolves age-encrypted material.
package passphrase

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
)

// AgeBackend resolves age:, age-b64:, and age-file: refs when identities are configured.
type AgeBackend struct {
	ids       []age.Identity
	recipeDir func(context.Context) string
}

// NewAge returns an age backend; recipeDir is used for age-file: relative paths (may be nil).
func NewAge(ids []age.Identity, recipeDir func(context.Context) string) ref.Backend {
	return &AgeBackend{ids: ids, recipeDir: recipeDir}
}

// Name implements [ref.Backend].
func (a *AgeBackend) Name() string { return "age" }

// Handles implements [ref.Backend].
func (a *AgeBackend) Handles(ref string) bool {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "age-file:"), strings.HasPrefix(ref, "age-b64:"):
		return true
	case strings.HasPrefix(ref, "age:"):
		return true
	default:
		return false
	}
}

// Resolve implements [ref.Backend].
func (a *AgeBackend) Resolve(ctx context.Context, ref string) (string, error) {
	if len(a.ids) == 0 {
		return "", fmt.Errorf("age backends require identities (HONEY_AGE_IDENTITY_FILE or resolver options)")
	}
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "age-b64:"):
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ref[len("age-b64:"):]))
		if err != nil {
			return "", fmt.Errorf("age-b64: decode: %w", err)
		}
		return decryptAgeBytes(a.ids, b)
	case strings.HasPrefix(ref, "age-file:"):
		rel := strings.TrimSpace(ref[len("age-file:"):])
		if rel == "" {
			return "", fmt.Errorf("age-file: missing path")
		}
		var base string
		if a.recipeDir != nil {
			base = a.recipeDir(ctx)
		}
		if base == "" {
			return "", fmt.Errorf("age-file: recipe directory unknown (internal error)")
		}
		abs := filepath.Clean(filepath.Join(base, rel))
		if !strings.HasPrefix(abs, filepath.Clean(base)+string(os.PathSeparator)) && abs != filepath.Clean(base) {
			return "", fmt.Errorf("age-file: path escapes recipe directory")
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("age-file: %w", err)
		}
		return decryptAgeBytes(a.ids, b)
	case strings.HasPrefix(ref, "age:"):
		payload := []byte(strings.TrimSpace(ref[len("age:"):]))
		if len(payload) == 0 {
			return "", fmt.Errorf("age: missing ciphertext")
		}
		return decryptAgeBytes(a.ids, payload)
	default:
		return "", fmt.Errorf("age: unsupported ref")
	}
}

func decryptAgeBytes(ids []age.Identity, armored []byte) (string, error) {
	r, err := age.Decrypt(bytes.NewReader(armored), ids...)
	if err != nil {
		return "", fmt.Errorf("age decrypt: %w", err)
	}
	var out strings.Builder
	if _, err := io.Copy(&out, r); err != nil {
		return "", err
	}
	return out.String(), nil
}
