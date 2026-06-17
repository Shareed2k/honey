package proxmoxprovider

import (
	"testing"

	"github.com/shareed2k/honey/internal/config"
)

func TestProxmoxBackendByName(t *testing.T) {
	t.Parallel()
	cfg := &config.File{
		Version: 1,
		Backends: config.Backends{
			Proxmox: []config.ProxmoxBackend{
				{Name: "a", URL: "https://pve:8006/", TokenID: "u!t", TokenSecret: "s", ExecMode: "pve"},
			},
		},
	}
	config.Set(cfg)
	reconfigureProxmox()
	b, ok := BackendByName("a")
	if !ok || b.ExecMode != ProxmoxExecPVE {
		t.Fatalf("got ok=%v mode=%q", ok, b.ExecMode)
	}
	config.Set(&config.File{})
	reconfigureProxmox()
	if _, ok := BackendByName("a"); ok {
		t.Fatal("expected cleared backends")
	}
}
