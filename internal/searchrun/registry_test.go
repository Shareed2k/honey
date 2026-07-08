package searchrun

import (
	"context"
	"reflect"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

// mockProviderBackend implements hosts.Backend
type mockProviderBackend struct {
	id string
}

func (m *mockProviderBackend) ID() string            { return m.id }
func (m *mockProviderBackend) BackendName() string   { return m.id }
func (m *mockProviderBackend) CacheIdentity() string { return m.id }
func (m *mockProviderBackend) Search(context.Context, hosts.Query) ([]hosts.Record, error) {
	return nil, nil
}

// mockFactory implements ProviderFactory and BackendConfigRegistry and FlagRegistrar
type mockFactory struct {
	id     string
	kind   string
	rows   []config.BackendRow
	config []string // mock slice
}

func (m *mockFactory) FromConfig(_ ProviderOverrides) []hosts.Backend {
	return []hosts.Backend{&mockProviderBackend{id: m.id}}
}

func (m *mockFactory) Default(_ ProviderOverrides) hosts.Backend {
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

func (m *mockFactory) RegisterFlags(cmd *cobra.Command) {
	cmd.Flags().String("mock-flag-"+m.id, "", "mock flag")
}

func TestRegistry_ListSearchProviderIDs(t *testing.T) {
	f1 := &mockFactory{id: "mock1"}
	f2 := &mockFactory{id: "mock2"}
	reg := NewRegistry([]ProviderFactory{f1, f2})

	ids := reg.ListSearchProviderIDs(nil)
	assert.Equal(t, []string{"mock1", "mock2"}, ids)
}

func TestRegistry_ListBackendRows(t *testing.T) {
	// Set global config
	cfg := &config.File{}
	config.Set(cfg)

	f1 := &mockFactory{
		id: "mock1",
		rows: []config.BackendRow{
			{Name: "b1", Kind: "mock"},
		},
	}
	reg := NewRegistry([]ProviderFactory{f1})
	rows := reg.ListBackendRows()
	assert.Len(t, rows, 1)
	assert.Equal(t, "b1", rows[0].Name)
}

func TestRegistry_RegisterAllProviderFlags(t *testing.T) {
	f1 := &mockFactory{id: "mock1"}
	reg := NewRegistry([]ProviderFactory{f1})

	cmd := &cobra.Command{}
	reg.RegisterAllProviderFlags(cmd)

	assert.NotNil(t, cmd.Flags().Lookup("mock-flag-mock1"))
}

func TestRegistry_BackendConfigRegistry(t *testing.T) {
	f1 := &mockFactory{kind: "mock1", config: []string{"cfg1"}}
	f2 := &mockFactory{kind: "mock2", config: []string{"cfg2"}}
	reg := NewRegistry([]ProviderFactory{f1, f2})

	// Test RegisteredBackendKinds
	kinds := reg.RegisteredBackendKinds()
	assert.Equal(t, []string{"mock1", "mock2"}, kinds)

	// Test GetBackendSliceByKind with config set
	config.Set(&config.File{})
	slice, err := reg.GetBackendSliceByKind("mock1")
	assert.NoError(t, err)
	assert.True(t, slice.IsValid())
	assert.Equal(t, reflect.Slice, slice.Kind())
	// underlying is []string{"cfg1"}
	assert.Equal(t, 1, slice.Len())
	assert.Equal(t, "cfg1", slice.Index(0).String())

	// Test GetBackendSliceByKind for unknown kind
	_, err = reg.GetBackendSliceByKind("unknown")
	assert.ErrorContains(t, err, "unknown backend kind")
}

// badFactory tests invalid BackendSlicePtr
type badFactory struct {
	mockFactory
}

func (b *badFactory) BackendSlicePtr() any {
	return "not a pointer to slice"
}

func TestRegistry_BadBackendSlicePtr(t *testing.T) {
	f := &badFactory{mockFactory: mockFactory{kind: "bad"}}
	reg := NewRegistry([]ProviderFactory{f})

	config.Set(&config.File{})
	slice, err := reg.GetBackendSliceByKind("bad")
	assert.ErrorContains(t, err, "must return a slice")
	assert.False(t, slice.IsValid())
}
