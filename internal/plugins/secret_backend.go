package plugins

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

var _ ref.Backend = SecretBackend{}

// SecretBackend resolves secret refs via a WASM plugin prefix.
type SecretBackend struct {
	mgr      *Manager
	pluginID string
	prefix   string
}

// SecretRefBackends returns ref.Backend adapters for plugins with the secret capability.
func (m *Manager) SecretRefBackends() []ref.Backend {
	bs := m.secretBackends()
	out := make([]ref.Backend, len(bs))
	for i := range bs {
		out[i] = bs[i]
	}
	return out
}

func (m *Manager) secretBackends() []SecretBackend {
	if m == nil {
		return nil
	}
	var out []SecretBackend
	for _, p := range m.plugins {
		if !p.manifest.hasCapability(CapSecret) {
			continue
		}
		for _, pref := range p.manifest.SecretRefPrefixes {
			pref = strings.TrimSpace(pref)
			if pref == "" {
				continue
			}
			out = append(out, SecretBackend{mgr: m, pluginID: p.manifest.ID, prefix: pref})
		}
	}
	return out
}

// Name implements ref.Backend.
func (b SecretBackend) Name() string {
	return "plugin:" + b.pluginID
}

// Handles implements ref.Backend.
func (b SecretBackend) Handles(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), b.prefix)
}

// Resolve implements ref.Backend.
func (b SecretBackend) Resolve(ctx context.Context, ref string) (string, error) {
	in := apiv1.ResolveSecretInput{
		APIVersion: apiv1.APIVersion,
		Ref:        ref,
		PluginID:   b.pluginID,
	}
	var out apiv1.ResolveSecretOutput
	if err := b.mgr.Call(ctx, b.pluginID, "resolve_secret", in, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Value) == "" {
		return "", fmt.Errorf("plugins: %s: empty secret value", b.pluginID)
	}
	return out.Value, nil
}
