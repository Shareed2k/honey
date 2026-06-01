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
	// Snapshot the current factories list, append a known stub, then restore.
	before := factories
	t.Cleanup(func() { factories = before })

	stub := stubFactory{id: "test-stub"}
	factories = append(factories, stub)

	backends := BuildProviders(nil, ProviderOverrides{})

	if len(backends) != len(factories) {
		t.Errorf("expected %d backends (one per factory), got %d", len(factories), len(backends))
	}
}
