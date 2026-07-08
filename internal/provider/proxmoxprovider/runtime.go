package proxmoxprovider

import (
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/provider/backendruntime"
)

// ProxmoxExecMode controls how honey runs commands against Proxmox guests.
type ProxmoxExecMode string

const (
	// ProxmoxExecSSH runs commands and file ops over guest SSH (default).
	ProxmoxExecSSH ProxmoxExecMode = "ssh"
	// ProxmoxExecPVE uses the Proxmox API where supported (QEMU guest agent; LXC uses guest SSH for exec, PVE console for TTY).
	ProxmoxExecPVE ProxmoxExecMode = "pve"
	// ProxmoxExecHybrid uses API for QEMU exec / LXC exec path and SSH for file ops and tunnels where applicable.
	ProxmoxExecHybrid ProxmoxExecMode = "hybrid"
)

// ProxmoxBackendRuntime holds in-memory Proxmox API credentials (never put secrets in hosts.Record JSON).
type ProxmoxBackendRuntime struct {
	Name     string
	ExecMode ProxmoxExecMode
	URL      string
	User     string
	Password string
	TokenID  string
	TokenSec string
	Insecure bool
}

var rtReg = backendruntime.New(func(b ProxmoxBackendRuntime) string { return b.Name })

func reconfigureProxmox() {
	cfg := config.Get()
	if cfg == nil {
		rtReg.Reconfigure(nil)
		return
	}
	items := make([]ProxmoxBackendRuntime, 0, len(cfg.Backends.Proxmox))
	for _, e := range cfg.Backends.Proxmox {
		mode := ProxmoxExecMode(strings.ToLower(e.ExecMode))
		switch mode {
		case ProxmoxExecPVE, ProxmoxExecHybrid:
		default:
			mode = ProxmoxExecSSH
		}
		items = append(items, ProxmoxBackendRuntime{
			Name:     e.Name,
			ExecMode: mode,
			URL:      e.URL,
			User:     e.User,
			Password: e.Password,
			TokenID:  e.TokenID,
			TokenSec: e.TokenSecret,
			Insecure: e.Insecure,
		})
	}
	rtReg.Reconfigure(items)
}

// BackendByName returns API runtime config for a named Proxmox backend (empty name matches first entry).
func BackendByName(name string) (ProxmoxBackendRuntime, bool) {
	return rtReg.ByName(name)
}
