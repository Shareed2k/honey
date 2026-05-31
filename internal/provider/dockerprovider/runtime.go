package dockerprovider

import (
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

// DockerBackendRuntime holds Docker API connection settings.
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

// DockerSSHBorrower returns a shared SSH client for a hop record when available.
type DockerSSHBorrower func(user string, hop hosts.Record) (*ssh.Client, bool)

var (
	rtMu        sync.RWMutex
	dockerBack  []DockerBackendRuntime
	sshBorrower DockerSSHBorrower
)

func reconfigureDocker(cfg *config.File) {
	rtMu.Lock()
	defer rtMu.Unlock()
	dockerBack = dockerBack[:0]
	if cfg == nil {
		return
	}
	locals := cfg.Backends.Local
	for _, e := range cfg.Backends.Docker {
		rt := DockerBackendRuntime{
			Name:          e.Name,
			Host:          e.Host,
			ViaLocal:      e.ViaLocal,
			ViaSSH:        e.ViaSSH,
			Socket:        e.Socket,
			Platform:      e.Platform,
			RunAs:         e.RunAs,
			LocalBackends: locals,
			TLSVerify:     e.TLSVerify,
			CACert:        e.CACert,
			Cert:          e.Cert,
			Key:           e.Key,
		}
		if e.ViaLocal != "" || e.ViaSSH.Host != "" {
			rt.Transport = "honey_ssh"
		}
		dockerBack = append(dockerBack, rt)
	}
}

// BackendByName returns runtime config for a named Docker backend (empty name matches first entry).
func BackendByName(name string) (DockerBackendRuntime, bool) {
	rtMu.RLock()
	defer rtMu.RUnlock()
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

// RegisterDockerSSHBorrower registers an optional SSH client borrower for honey-ssh Docker transport.
func RegisterDockerSSHBorrower(fn DockerSSHBorrower) {
	rtMu.Lock()
	defer rtMu.Unlock()
	sshBorrower = fn
}

// BorrowDockerSSH returns a shared SSH client when a borrower is registered and has a match.
func BorrowDockerSSH(user string, hop hosts.Record) (*ssh.Client, bool) {
	rtMu.RLock()
	fn := sshBorrower
	rtMu.RUnlock()
	if fn == nil {
		return nil, false
	}
	return fn(user, hop)
}
