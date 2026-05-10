package hostexec

import (
	"testing"

	"github.com/shareed2k/honey/internal/config"
)

func TestReconfigureFromHoneyConfigProxmoxExecMode(t *testing.T) {
	t.Parallel()
	cfg := &config.File{
		Version: 1,
		Backends: config.Backends{
			Proxmox: []config.ProxmoxBackend{
				{Name: "a", URL: "https://pve:8006/", TokenID: "u!t", TokenSecret: "s", ExecMode: "pve"},
			},
		},
	}
	ReconfigureFromHoneyConfig(cfg)
	b, ok := ProxmoxBackendByName("a")
	if !ok || b.ExecMode != ProxmoxExecPVE {
		t.Fatalf("got ok=%v mode=%q", ok, b.ExecMode)
	}
	ReconfigureFromHoneyConfig(nil)
	if _, ok := ProxmoxBackendByName("a"); ok {
		t.Fatal("expected cleared backends")
	}
}
