package proxmoxprovider

import (
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/config"
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

var (
	rtMu        sync.RWMutex
	proxmoxBack []ProxmoxBackendRuntime
)

func reconfigureProxmox() {
	cfg := config.Get()
	rtMu.Lock()
	defer rtMu.Unlock()
	proxmoxBack = proxmoxBack[:0]
	if cfg == nil {
		return
	}
	for _, e := range cfg.Backends.Proxmox {
		mode := ProxmoxExecMode(strings.ToLower(e.ExecMode))
		switch mode {
		case ProxmoxExecPVE, ProxmoxExecHybrid:
		default:
			mode = ProxmoxExecSSH
		}
		proxmoxBack = append(proxmoxBack, ProxmoxBackendRuntime{
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
}

// BackendByName returns API runtime config for a named Proxmox backend (empty name matches first entry).
func BackendByName(name string) (ProxmoxBackendRuntime, bool) {
	rtMu.RLock()
	defer rtMu.RUnlock()
	name = strings.TrimSpace(name)
	if len(proxmoxBack) == 0 {
		return ProxmoxBackendRuntime{}, false
	}
	if name == "" {
		return proxmoxBack[0], true
	}
	for _, b := range proxmoxBack {
		if b.Name == name {
			return b, true
		}
	}
	return ProxmoxBackendRuntime{}, false
}
