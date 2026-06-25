//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/webserver"
	"github.com/shareed2k/honey/internal/provider/localprovider"
)

func TestHoneyProviderE2E(t *testing.T) {
	// 1. Create a real config file for the remote server with a "local" backend
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := `
version: 1
backends:
  local:
    - name: "prod"
      hosts:
        - name: "remote-host"
          primary_ip: "10.0.0.1"
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create a real search registry containing the local provider
	remoteSearchReg := searchrun.NewRegistry([]searchrun.ProviderFactory{
		localprovider.NewFactory(testLocalConfig{}),
	})

	// Boot up the real honey webserver
	opts := webserver.Options{
		SearchRegistry: remoteSearchReg,
		Token:          "test-token",
		ConfigPath:     configPath,
	}
	baseURL := newTestServer(t, opts)

	// Initialize the honey provider client pointing to the real webserver
	cfg := &config.File{
		Backends: config.Backends{
			Honey: []config.HoneyBackend{
				{Name: "remote1", URL: baseURL, Token: "test-token", Insecure: false},
			},
		},
	}

	classCfg := honeyTestConfig{cfg}
	factory := honeyprovider.NewFactory(classCfg)

	// Test 1: Fetch BackendRows (should hit /api/v1/config/backends)
	rows := factory.BackendRows()
	if len(rows) != 2 {
		t.Fatalf("expected 2 backend rows (1 honey, 1 remote), got %d: %+v", len(rows), rows)
	}
	if rows[0].Kind != "honey" || rows[0].Name != "remote1" {
		t.Errorf("expected honey row first")
	}
	if rows[1].Kind != "local" || rows[1].Name != "prod" {
		t.Errorf("expected remote backend row second")
	}

	// Test 2: Search Hosts (should hit /api/v1/search)
	provider := factory.FromConfig(nil)[0]
	recs, err := provider.Search(context.Background(), hosts.Query{NameSubstring: "remote"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Name != "remote-host" {
		t.Errorf("unexpected record: %+v", recs[0])
	}
}

type honeyTestConfig struct {
	cfg *config.File
}

func (h honeyTestConfig) HoneyBackends() []config.HoneyBackend {
	return h.cfg.Backends.Honey
}

func (h honeyTestConfig) HoneyBackendSlicePtr() *[]config.HoneyBackend {
	return &h.cfg.Backends.Honey
}

func (h honeyTestConfig) SetHoneyBackends(b []config.HoneyBackend) {
	h.cfg.Backends.Honey = b
}

type testLocalConfig struct{}

func (testLocalConfig) LocalBackends() []config.LocalBackend {
	return []config.LocalBackend{
		{
			Name: "prod",
			Hosts: []config.LocalHost{
				{Name: "remote-host", PrimaryIP: "10.0.0.1"},
			},
		},
	}
}
func (testLocalConfig) LocalBackendSlicePtr() *[]config.LocalBackend { return nil }
func (testLocalConfig) SetLocalBackends([]config.LocalBackend)       {}
func (testLocalConfig) DockerDiscover() config.DockerDiscover        { return config.DockerDiscover{} }
