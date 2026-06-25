//go:build integration

package integration

import (
	"context"
	"net/http/httptest"
	"testing"
	"net/http"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
)

func TestHoneyProviderE2E(t *testing.T) {
	// Create a mock remote Honey server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/search" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"records":[{"provider":"test-cloud","name":"remote-host","primary_ip":"10.0.0.1"}]}`))
			return
		}
		if r.URL.Path == "/api/v1/config/backends" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"config_path":"/tmp/cfg","backends":[{"kind":"test-cloud","name":"prod","hint":"hint"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// Initialize the honey provider
	cfg := &config.File{
		Backends: config.Backends{
			Honey: []config.HoneyBackend{
				{Name: "remote1", URL: srv.URL, Insecure: false},
			},
		},
	}

	classCfg := honeyTestConfig{cfg}
	factory := honeyprovider.NewFactory(classCfg)

	// Test 1: Fetch BackendRows
	rows := factory.BackendRows()
	if len(rows) != 2 {
		t.Fatalf("expected 2 backend rows (1 honey, 1 remote), got %d: %+v", len(rows), rows)
	}
	if rows[0].Kind != "honey" || rows[0].Name != "remote1" {
		t.Errorf("expected honey row first")
	}
	if rows[1].Kind != "test-cloud" || rows[1].Name != "prod" {
		t.Errorf("expected remote backend row second")
	}

	// Test 2: Search Hosts
	provider := factory.FromConfig(nil)[0]
	recs, err := provider.Search(context.Background(), hosts.Query{NameSubstring: "remote"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Name != "remote-host" || recs[0].Provider != "test-cloud" {
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
