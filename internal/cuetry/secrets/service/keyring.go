// Package service resolves keyring:// refs via the OS credential store (Zalando keyring),
// analogous to cloud:/aws-sm:/aws-kms:/k8s:/age:/age-b64:/age-file:/keyring:/vault.
package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
	"github.com/zalando/go-keyring"
)

// Backend implements [ref.Backend] for keyring://service/user.
type Backend struct{}

// New returns a keyring backend.
func New() ref.Backend { return Backend{} }

// Name implements [ref.Backend].
func (Backend) Name() string { return "keyring" }

// Handles implements [ref.Backend].
func (Backend) Handles(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), "keyring://")
}

// Resolve implements [ref.Backend].
func (Backend) Resolve(_ context.Context, ref string) (string, error) {
	rest := strings.TrimSpace(ref[len("keyring://"):])
	serviceName, user, ok := strings.Cut(rest, "/")
	serviceName, user = strings.TrimSpace(serviceName), strings.TrimSpace(user)
	if !ok || serviceName == "" || user == "" {
		return "", fmt.Errorf("keyring ref must be keyring://service/user")
	}
	v, err := keyring.Get(serviceName, user)
	if err != nil {
		return "", fmt.Errorf("keyring://%s/%s: %w", serviceName, user, err)
	}
	return v, nil
}
