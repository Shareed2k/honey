package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/truenasshell"
)

func TestLoadHostexecFromHoneyConfig_truenasBackend(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(`version: 1
backends:
  truenas:
    - name: lab
      url: https://nas.example.com
      api_key: "1-secret"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { GetSearchRegistry().ReconfigureFromConfig() })

	if err := loadHostexecFromHoneyConfig(cfgPath); err != nil {
		t.Fatal(err)
	}
	if _, ok := truenasprovider.BackendByName("lab"); !ok {
		t.Fatal("expected truenas backend lab after load")
	}
	rec := hosts.Record{
		Provider: "truenas",
		Meta: map[string]string{
			"kind":         "appliance",
			"backend_name": "lab",
		},
	}
	if !truenasshell.ShouldUseTrueNASShell(rec, truenasshell.ConsoleTrueNASAPI) {
		t.Fatal("expected ShouldUseTrueNASShell after config load")
	}
}
