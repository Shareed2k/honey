package secrets

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
)

// Manager coordinates resolution across multiple [ref.Backend] instances (analogous to
type Manager struct {
	backends []ref.Backend
}

// NewManager builds a manager from an explicit backend list (tests may inject mocks).
func NewManager(backends ...ref.Backend) *Manager {
	return &Manager{backends: append([]ref.Backend(nil), backends...)}
}

// Resolve implements [Resolver].
func (m *Manager) Resolve(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty secret ref")
	}
	for _, b := range m.backends {
		if b.Handles(ref) {
			return b.Resolve(ctx, ref)
		}
	}
	return "", fmt.Errorf("unsupported secret ref (only symmetric secure:v1:…): %q", ref)
}
