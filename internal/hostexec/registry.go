package hostexec

import (
	"context"
	"io"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"

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

// DockerBackendRuntime holds Docker API connection settings (TLS paths stay in config, not records).
type DockerBackendRuntime struct {
	Name          string
	Host          string
	ViaLocal      string
	ViaSSH        config.DockerViaSSH
	Socket        string
	Platform      string
	RunAs         string
	Transport     string
	LocalBackends []config.LocalBackend
	TLSVerify     bool
	CACert        string
	Cert          string
	Key           string
}

// DockerSSHBorrower returns a shared SSH client for a hop record when available (e.g. TUI cache).
type DockerSSHBorrower func(user string, hop hosts.Record) (*ssh.Client, bool)

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
	regMu            sync.RWMutex
	proxmoxBack      []ProxmoxBackendRuntime
	dockerBack       []DockerBackendRuntime
	configuredLocals []config.LocalBackend

	k8sExecutor    Executor
	dockerExecutor Executor

	// dialHoneyHost connects to PrimaryIP (or alias) via SSH; wired by internal/sshclient init.
	dialHoneyHost func(user, hostAlias string, overridePort int, identityFile string) (HostClient, error)

	// sshRunInteractive opens a local TTY session; wired by internal/ui init.
	sshRunInteractive func(user string, r hosts.Record, recorder any) error

	// sshRunTunnel runs SSH -L style forwarding; wired by internal/sshclient init.
	sshRunTunnel func(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error

	proxmoxPickExecutor func(r hosts.Record) Executor

	dockerSSHBorrower DockerSSHBorrower
)

// SetK8sExecutor registers the Kubernetes pod executor (typically from ui.init).
func SetK8sExecutor(ex Executor) {
	regMu.Lock()
	defer regMu.Unlock()
	k8sExecutor = ex
}

// SetDockerExecutor registers the Docker container executor (typically from ui.init).
func SetDockerExecutor(ex Executor) {
	regMu.Lock()
	defer regMu.Unlock()
	dockerExecutor = ex
}

// SetDialHoney registers the SSH HostClient dialer (from sshclient.init).
func SetDialHoney(fn func(user, hostAlias string, overridePort int, identityFile string) (HostClient, error)) {
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
func SetSSHRunTunnel(fn func(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error) {
	regMu.Lock()
	defer regMu.Unlock()
	sshRunTunnel = fn
}

// RegisterDockerSSHBorrower registers an optional SSH client borrower for honey-ssh Docker transport.
func RegisterDockerSSHBorrower(fn DockerSSHBorrower) {
	regMu.Lock()
	defer regMu.Unlock()
	dockerSSHBorrower = fn
}

// BorrowDockerSSH returns a shared SSH client when a borrower is registered and has a match.
func BorrowDockerSSH(user string, hop hosts.Record) (*ssh.Client, bool) {
	regMu.RLock()
	fn := dockerSSHBorrower
	regMu.RUnlock()
	if fn == nil {
		return nil, false
	}
	return fn(user, hop)
}

// ConfiguredLocalBackends returns backends.local from the last ReconfigureFromHoneyConfig call.
func ConfiguredLocalBackends() []config.LocalBackend {
	regMu.RLock()
	defer regMu.RUnlock()
	if len(configuredLocals) == 0 {
		return nil
	}
	out := make([]config.LocalBackend, len(configuredLocals))
	copy(out, configuredLocals)
	return out
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
	dockerBack = dockerBack[:0]
	configuredLocals = nil
	if cfg == nil {
		return
	}
	locals := cfg.Backends.Local
	if len(locals) > 0 {
		configuredLocals = append([]config.LocalBackend(nil), locals...)
	}
	for _, e := range cfg.Backends.Docker {
		rt := DockerBackendRuntime{
			Name:          strings.TrimSpace(e.Name),
			Host:          strings.TrimSpace(e.Host),
			ViaLocal:      strings.TrimSpace(e.ViaLocal),
			ViaSSH:        e.ViaSSH,
			Socket:        strings.TrimSpace(e.Socket),
			Platform:      strings.TrimSpace(e.Platform),
			RunAs:         strings.TrimSpace(e.RunAs),
			LocalBackends: locals,
			TLSVerify:     e.TLSVerify,
			CACert:        strings.TrimSpace(e.CACert),
			Cert:          strings.TrimSpace(e.Cert),
			Key:           strings.TrimSpace(e.Key),
		}
		if strings.TrimSpace(e.ViaLocal) != "" || strings.TrimSpace(e.ViaSSH.Host) != "" {
			rt.Transport = "honey_ssh"
		}
		dockerBack = append(dockerBack, rt)
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

// DockerBackendByName returns runtime config for a named Docker backend (empty name matches first entry).
func DockerBackendByName(name string) (DockerBackendRuntime, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	name = strings.TrimSpace(name)
	if len(dockerBack) == 0 {
		return DockerBackendRuntime{}, false
	}
	if name == "" {
		return dockerBack[0], true
	}
	for _, b := range dockerBack {
		if b.Name == name {
			return b, true
		}
	}
	return DockerBackendRuntime{}, false
}

// RunSSHTunnel runs the SSH local-forward tunnel registered by sshclient (used for Proxmox hybrid/pve tunnel fallback).
func RunSSHTunnel(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error {
	regMu.RLock()
	fn := sshRunTunnel
	regMu.RUnlock()
	if fn == nil {
		return errTunnelNotConfigured
	}
	return fn(ctx, user, host, sshPort, localFwd, out)
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
	override := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		override = p
	}
	identity := ""
	if id, ok := hosts.MetaSSHIdentityFile(&r); ok {
		identity = id
	}
	return dialHoneyHost(user, host, override, identity)
}

func (sshExecutor) RunInteractive(user string, r hosts.Record) error {
	if sshRunInteractive == nil {
		return errInteractiveNotConfigured
	}
	return sshRunInteractive(user, r, nil)
}

func (sshExecutor) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	if sshRunTunnel == nil {
		return errTunnelNotConfigured
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return errNoHostIP
	}
	override := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		override = p
	}
	return sshRunTunnel(ctx, user, host, override, localFwd, out)
}

var defaultSSHExecutor = sshExecutor{}

func dockerRecordKind(r hosts.Record) bool {
	if r.Provider != "docker" {
		return false
	}
	k := strings.ToLower(strings.TrimSpace(r.Meta["kind"]))
	return k == "container" || k == "swarm_task"
}

// ForRecord returns the Executor for a search row (SSH to IP, k8s exec, or Proxmox API when configured).
func ForRecord(r hosts.Record) Executor {
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" && k8sExecutor != nil {
		return k8sExecutor
	}
	regMu.RLock()
	dex := dockerExecutor
	pick := proxmoxPickExecutor
	regMu.RUnlock()
	if r.Provider == "docker" && dockerRecordKind(r) && dex != nil {
		return dex
	}
	if pick != nil {
		if ex := pick(r); ex != nil {
			return ex
		}
	}
	return defaultSSHExecutor
}
