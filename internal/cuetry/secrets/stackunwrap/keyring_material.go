package stackunwrap

import (
	"encoding/base64"
	"fmt"
	"strings"
)

const keyringStackKeyBytes = 32

// EncodeKeyringMaterial stores a stack data key as standard base64 for OS keyrings.
func EncodeKeyringMaterial(key []byte) (string, error) {
	if len(key) != keyringStackKeyBytes {
		return "", fmt.Errorf("keyring: expected %d-byte key, got %d", keyringStackKeyBytes, len(key))
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// DecodeKeyringMaterial reads a keyring value (base64 or legacy raw 32 bytes).
func DecodeKeyringMaterial(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, fmt.Errorf("keyring: empty secret value")
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil && len(b) > 0 {
		return b, nil
	}
	return []byte(v), nil
}
