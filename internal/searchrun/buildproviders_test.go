package searchrun

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

// stubFactory is a minimal ProviderFactory used solely within this test to
// exercise BuildProviders without triggering an import cycle (provider
// packages import searchrun, so they cannot be imported from within searchrun
// tests themselves).
type stubFactory struct{ id string }

func (s stubFactory) Default(_ ProviderOverrides) hosts.Backend                      { return stubBackend(s) }
func (s stubFactory) FromConfig(_ *config.File, _ ProviderOverrides) []hosts.Backend { return nil }
func (s stubFactory) BackendRows(_ *config.File) []config.BackendRow                 { return nil }

type stubBackend struct{ id string }

func (s stubBackend) ID() string            { return s.id }
func (s stubBackend) BackendName() string   { return s.id }
func (s stubBackend) CacheIdentity() string { return s.id }
func (s stubBackend) Search(_ context.Context, _ hosts.Query) ([]hosts.Record, error) {
	return nil, nil
}

func TestBuildProviders_ReturnsOnePerFactory(t *testing.T) {
	stub := stubFactory{id: "test-stub"}
	reg := NewRegistry([]ProviderFactory{stub})

	backends := reg.BuildProviders(nil, ProviderOverrides{})

	if len(backends) != 1 {
		t.Errorf("expected 1 backends (one per factory), got %d", len(backends))
	}
}
