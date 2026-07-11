package hostapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// mockProviderBackend implements hosts.Backend
type mockProviderBackend struct {
	id string
}

func (m *mockProviderBackend) ID() string            { return m.id }
func (m *mockProviderBackend) BackendName() string   { return m.id }
func (m *mockProviderBackend) CacheIdentity() string { return m.id }
func (m *mockProviderBackend) Search(context.Context, hosts.Query) ([]hosts.Record, error) {
	return []hosts.Record{{Name: "test-record"}}, nil
}

// mockFactory implements ProviderFactory and BackendConfigRegistry
type mockFactory struct {
	id     string
	kind   string
	rows   []config.BackendRow
	config []string // mock slice
}

func (m *mockFactory) FromConfig(_ searchrun.ProviderOverrides) []hosts.Backend {
	return []hosts.Backend{&mockProviderBackend{id: m.id}}
}

func (m *mockFactory) Default(_ searchrun.ProviderOverrides) hosts.Backend {
	return &mockProviderBackend{id: m.id}
}

func (m *mockFactory) BackendRows() []config.BackendRow {
	return m.rows
}

func (m *mockFactory) BackendKind() string {
	return m.kind
}

func (m *mockFactory) BackendSlicePtr() any {
	return &m.config
}

func TestListBackends(t *testing.T) {
	// Create a dummy config
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "honeyfile.yaml")
	err := os.WriteFile(cfgPath, []byte(""), 0o644)
	require.NoError(t, err)

	f1 := &mockFactory{
		id: "mock1",
		rows: []config.BackendRow{
			{Name: "b1", Kind: "mock"},
		},
	}
	reg := searchrun.NewRegistry([]searchrun.ProviderFactory{f1})

	t.Run("success", func(t *testing.T) {
		out, err := ListBackends(cfgPath, reg)
		assert.NoError(t, err)
		assert.Equal(t, cfgPath, out.ConfigPath)
		assert.Len(t, out.Backends, 1)
		assert.Equal(t, "b1", out.Backends[0].Name)
	})

	t.Run("missing_config", func(t *testing.T) {
		out, err := ListBackends("missing.yaml", reg)
		assert.ErrorContains(t, err, "no such file or directory")
		assert.Empty(t, out.ConfigPath)
	})

	t.Run("invalid_config", func(t *testing.T) {
		badCfgPath := filepath.Join(tmpDir, "bad.yaml")
		err := os.WriteFile(badCfgPath, []byte("invalid yaml content: ["), 0o644)
		require.NoError(t, err)

		_, err = ListBackends(badCfgPath, reg)
		assert.ErrorContains(t, err, "config:")
	})
}
