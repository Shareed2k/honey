package plugins

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry/secrets"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

// SecretBackend resolves secret refs via a WASM plugin prefix.
type SecretBackend struct {
	mgr      *Manager
	pluginID string
	prefix   string
}

// SecretRefBackends returns ExtraBackend functions for plugins with the secret capability.
func (m *Manager) SecretRefBackends() []secrets.ExtraBackend {
	bs := m.secretBackends()
	out := make([]secrets.ExtraBackend, len(bs))
	for i := range bs {
		b := bs[i]
		out[i] = func(ctx context.Context, ref string) (string, bool, error) {
			if !strings.HasPrefix(strings.TrimSpace(ref), b.prefix) {
				return "", false, nil
			}
			in := apiv1.ResolveSecretInput{
				APIVersion: apiv1.APIVersion,
				Ref:        ref,
				PluginID:   b.pluginID,
			}
			var out apiv1.ResolveSecretOutput
			if err := b.mgr.Call(ctx, b.pluginID, "resolve_secret", in, &out); err != nil {
				return "", true, err
			}
			if strings.TrimSpace(out.Value) == "" {
				return "", true, fmt.Errorf("plugins: %s: empty secret value", b.pluginID)
			}
			return out.Value, true, nil
		}
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
