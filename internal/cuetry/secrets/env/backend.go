// Package env resolves env:NAME refs from the process environment (local analogue of
// cloud:/aws-sm:/aws-kms:/k8s:/age:/age-b64:/age-file:/keyring:/vault).
package env

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
)

// Backend implements [ref.Backend] for env:VAR.
type Backend struct{}

// New returns an env backend.
func New() ref.Backend { return Backend{} }

// Name implements [ref.Backend].
func (Backend) Name() string { return "env" }

// Handles implements [ref.Backend].
func (Backend) Handles(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), "env:")
}

// Resolve implements [ref.Backend].
func (Backend) Resolve(_ context.Context, ref string) (string, error) {
	name := strings.TrimSpace(ref[len("env:"):])
	if name == "" {
		return "", fmt.Errorf("env: ref missing variable name")
	}
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("env:%s: not set or empty", name)
	}
	return v, nil
}
