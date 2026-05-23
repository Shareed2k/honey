package truenasshell

import (
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

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
	hostexec.ReconfigureFromHoneyConfig(cfg)
	defer hostexec.ReconfigureFromHoneyConfig(nil)

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
