package dockerprovider

import (
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/config"
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

var (
	rtMu       sync.RWMutex
	dockerBack []DockerBackendRuntime
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
