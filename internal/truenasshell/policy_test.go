package truenasshell

import (
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/searchrun"
)

type dummyConfigAdapter struct{}

func (d dummyConfigAdapter) TrueNASBackends() []config.TrueNASBackend {
	return config.Get().Backends.TrueNAS
}

func (d dummyConfigAdapter) TrueNASBackendSlicePtr() *[]config.TrueNASBackend {
	cfg := config.Get()
	return &cfg.Backends.TrueNAS
}

func (d dummyConfigAdapter) SetTrueNASBackends(b []config.TrueNASBackend) {
	config.Get().Backends.TrueNAS = b
}

func (d dummyConfigAdapter) DockerDiscover() config.DockerDiscover {
	return config.DockerDiscover{}
}

func TestShouldUseTrueNASShell(t *testing.T) {
	t.Parallel()
	cfg := &config.File{
		Version: 1,
		Backends: config.Backends{
			TrueNAS: []config.TrueNASBackend{
				{Name: "lab", URL: "https://nas.example.com", APIKey: "1-secret"},
			},
		},
	}
	config.Set(cfg)
	reg := searchrun.NewRegistry([]searchrun.ProviderFactory{truenasprovider.NewFactory(nil, nil, dummyConfigAdapter{})})
	reg.ReconfigureFromConfig()
	defer config.Set(&config.File{})

	rec := hosts.Record{
		Provider: "truenas",
		Meta: map[string]string{
			"kind":         "appliance",
			"backend_name": "lab",
		},
	}
	if !ShouldUseTrueNASShell(rec, ConsoleTrueNASAPI) {
		t.Fatal("expected true for appliance + truenas_api")
	}
	if ShouldUseTrueNASShell(rec, "") {
		t.Fatal("expected false without console")
	}
	rec2 := hosts.Record{
		Provider: "truenas",
		Meta: map[string]string{
			"kind":         "appliance",
			"backend_name": "missing",
		},
	}
	if ShouldUseTrueNASShell(rec2, ConsoleTrueNASAPI) {
		t.Fatal("expected false for unknown backend")
	}
}
