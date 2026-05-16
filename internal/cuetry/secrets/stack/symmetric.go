package stack

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SymmetricKeyBytes is the AES-256 key size for secure:v1.
const SymmetricKeyBytes = 32

// DecryptSymmetricV1 decrypts inner form "v1:<base64-nonce>:<base64-ciphertext>".
func DecryptSymmetricV1(key []byte, value string) (string, error) {
	if len(key) != SymmetricKeyBytes {
		return "", fmt.Errorf("stack data key: expected %d bytes, got %d", SymmetricKeyBytes, len(key))
	}
	vals := strings.Split(value, ":")
	if len(vals) != 3 {
		return "", errors.New("secure: expected inner form v1:<base64-nonce>:<base64-ciphertext>")
	}
	if vals[0] != "v1" {
		return "", fmt.Errorf("secure: unknown version %q (expected v1)", vals[0])
	}
	nonce, err := base64.StdEncoding.DecodeString(vals[1])
	if err != nil {
		return "", fmt.Errorf("secure: bad nonce base64: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(vals[2])
	if err != nil {
		return "", fmt.Errorf("secure: bad ciphertext base64: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("secure: aes: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("secure: gcm: %w", err)
	}
	if len(nonce) != aesgcm.NonceSize() {
		return "", fmt.Errorf("secure: nonce size want %d got %d", aesgcm.NonceSize(), len(nonce))
	}
	msg, err := aesgcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("secure: decrypt: %w", err)
	}
	return string(msg), nil
}

// EncryptSymmetricV1 encrypts plaintext with key; inner form is v1:<nonce-b64>:<ct-b64>.
func EncryptSymmetricV1(key []byte, plaintext string) (string, error) {
	if len(key) != SymmetricKeyBytes {
		return "", fmt.Errorf("stack data key: expected %d bytes, got %d", SymmetricKeyBytes, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aesgcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := aesgcm.Seal(nil, nonce, []byte(plaintext), nil)
	return fmt.Sprintf("v1:%s:%s",
		base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(ct),
	), nil
}

// FormatSecureRef returns a full recipe ref "secure:v1:…".
func FormatSecureRef(key []byte, plaintext string) (string, error) {
	inner, err := EncryptSymmetricV1(key, plaintext)
	if err != nil {
		return "", err
	}
	return "secure:" + inner, nil
}

// ValidateSecureRef checks recipe secret values are secure:v1:… with decodable segments.
func ValidateSecureRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "secure:") {
		return fmt.Errorf("secret ref must use secure prefix")
	}
	inner := strings.TrimSpace(ref[len("secure:"):])
	vals := strings.Split(inner, ":")
	if len(vals) != 3 || vals[0] != "v1" {
		return fmt.Errorf("expected secure:v1:<nonce-b64>:<ciphertext-b64>")
	}
	if _, err := base64.StdEncoding.DecodeString(vals[1]); err != nil {
		return fmt.Errorf("secure: bad nonce base64: %w", err)
	}
	if _, err := base64.StdEncoding.DecodeString(vals[2]); err != nil {
		return fmt.Errorf("secure: bad ciphertext base64: %w", err)
	}
	return nil
}
