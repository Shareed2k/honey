package proxmoxprovider

import (
	"strings"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

func resolveProxmoxExecutor(r hosts.Record) hostexec.Executor {
	if r.Provider != "proxmox" {
		return nil
	}
	b, ok := hostexec.ProxmoxBackendByName(r.Meta["backend_name"])
	if !ok {
		return nil
	}
	switch b.ExecMode {
	case hostexec.ProxmoxExecPVE, hostexec.ProxmoxExecHybrid:
		return &proxmoxExecutor{rt: b}
	default:
		return nil
	}
}

type proxmoxExecutor struct {
	rt hostexec.ProxmoxBackendRuntime
}

func (p *proxmoxExecutor) Dial(user string, r hosts.Record) (hostexec.HostClient, error) {
	kind := strings.ToLower(strings.TrimSpace(r.Meta["kind"]))
	switch kind {
	case "lxc":
		if p.rt.ExecMode == hostexec.ProxmoxExecHybrid {
			return dialHybridLXC(p.rt, user, r)
		}
		return dialPVELXC(p.rt, user, r)
	case "qemu":
		if p.rt.ExecMode == hostexec.ProxmoxExecHybrid {
			return dialHybridQEMU(p.rt, user, r)
		}
		return dialPVEQEMU(p.rt, r) // *qemuGuestClient implements hostexec.HostClient
	default:
		return nil, errProxmoxUnknownKind
	}
}

func (p *proxmoxExecutor) RunInteractive(_ string, _ hosts.Record) error {
	return errProxmoxNoInteractiveTTY
}

func (p *proxmoxExecutor) RunTunnel(user string, r hosts.Record, localFwd string) error {
	ip := strings.TrimSpace(r.PrimaryIP)
	if ip == "" {
		return errProxmoxNoIP
	}
	return hostexec.RunSSHTunnel(user, ip, localFwd)
}
