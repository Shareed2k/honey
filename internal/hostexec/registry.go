package hostexec

import (
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
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
	regMu       sync.RWMutex
	proxmoxBack []ProxmoxBackendRuntime

	k8sExecutor Executor

	// dialHoneyHost connects to PrimaryIP (or alias) via SSH; wired by internal/sshclient init.
	dialHoneyHost func(user, hostAlias string) (HostClient, error)

	// sshRunInteractive opens a local TTY session; wired by internal/ui init.
	sshRunInteractive func(user string, r hosts.Record, recorder any) error

	// sshRunTunnel runs SSH -L style forwarding; wired by internal/sshclient init.
	sshRunTunnel func(user, host, localFwd string) error

	proxmoxPickExecutor func(r hosts.Record) Executor
)

// SetK8sExecutor registers the Kubernetes pod executor (typically from ui.init).
func SetK8sExecutor(ex Executor) {
	regMu.Lock()
	defer regMu.Unlock()
	k8sExecutor = ex
}

// SetDialHoney registers the SSH HostClient dialer (from sshclient.init).
func SetDialHoney(fn func(user, hostAlias string) (HostClient, error)) {
	regMu.Lock()
	defer regMu.Unlock()
	dialHoneyHost = fn
}

// SetSSHRunInteractive registers the TTY interactive runner (from ui.init).
func SetSSHRunInteractive(fn func(user string, r hosts.Record, recorder any) error) {
	regMu.Lock()
	defer regMu.Unlock()
	sshRunInteractive = fn
}

// SetSSHRunTunnel registers the SSH local-forward tunnel runner (from sshclient.init).
func SetSSHRunTunnel(fn func(user, host, localFwd string) error) {
	regMu.Lock()
	defer regMu.Unlock()
	sshRunTunnel = fn
}

// RegisterProxmoxExecutor registers a resolver that returns a non-nil Executor when
// this process should use Proxmox API transport for the given record (from proxmoxprovider.init).
func RegisterProxmoxExecutor(fn func(r hosts.Record) Executor) {
	regMu.Lock()
	defer regMu.Unlock()
	proxmoxPickExecutor = fn
}

// ReconfigureFromHoneyConfig stores Proxmox backend credentials and exec modes for API transport.
// Safe to call from CLI after loading config and from the web server on startup.
func ReconfigureFromHoneyConfig(cfg *config.File) {
	regMu.Lock()
	defer regMu.Unlock()
	proxmoxBack = proxmoxBack[:0]
	if cfg == nil {
		return
	}
	for _, e := range cfg.Backends.Proxmox {
		mode := ProxmoxExecMode(strings.ToLower(strings.TrimSpace(e.ExecMode)))
		switch mode {
		case ProxmoxExecPVE, ProxmoxExecHybrid:
		default:
			mode = ProxmoxExecSSH
		}
		proxmoxBack = append(proxmoxBack, ProxmoxBackendRuntime{
			Name:     strings.TrimSpace(e.Name),
			ExecMode: mode,
			URL:      strings.TrimSpace(e.URL),
			User:     strings.TrimSpace(e.User),
			Password: e.Password,
			TokenID:  strings.TrimSpace(e.TokenID),
			TokenSec: e.TokenSecret,
			Insecure: e.Insecure,
		})
	}
}

// ProxmoxBackendByName returns API runtime config for a named Proxmox backend (empty name matches first entry).
func ProxmoxBackendByName(name string) (ProxmoxBackendRuntime, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
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

// RunSSHTunnel runs the SSH local-forward tunnel registered by sshclient (used for Proxmox hybrid/pve tunnel fallback).
func RunSSHTunnel(user, host, localFwd string) error {
	regMu.RLock()
	fn := sshRunTunnel
	regMu.RUnlock()
	if fn == nil {
		return errTunnelNotConfigured
	}
	return fn(user, host, localFwd)
}

type sshExecutor struct{}

func (sshExecutor) Dial(user string, r hosts.Record) (HostClient, error) {
	if dialHoneyHost == nil {
		return nil, errDialNotConfigured
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return nil, errNoHostIP
	}
	return dialHoneyHost(user, host)
}

func (sshExecutor) RunInteractive(user string, r hosts.Record) error {
	if sshRunInteractive == nil {
		return errInteractiveNotConfigured
	}
	return sshRunInteractive(user, r, nil)
}

func (sshExecutor) RunTunnel(user string, r hosts.Record, localFwd string) error {
	if sshRunTunnel == nil {
		return errTunnelNotConfigured
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return errNoHostIP
	}
	return sshRunTunnel(user, host, localFwd)
}

var defaultSSHExecutor = sshExecutor{}

// ForRecord returns the Executor for a search row (SSH to IP, k8s exec, or Proxmox API when configured).
func ForRecord(r hosts.Record) Executor {
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" && k8sExecutor != nil {
		return k8sExecutor
	}
	regMu.RLock()
	pick := proxmoxPickExecutor
	regMu.RUnlock()
	if pick != nil {
		if ex := pick(r); ex != nil {
			return ex
		}
	}
	return defaultSSHExecutor
}
