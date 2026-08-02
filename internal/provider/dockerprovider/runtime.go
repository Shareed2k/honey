package dockerprovider

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/provider/backendruntime"
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

var rtReg = backendruntime.New(func(b DockerBackendRuntime) string { return b.Name })

func reconfigureDocker() {
	cfg := config.Get()
	if cfg == nil {
		rtReg.Reconfigure(nil)
		return
	}
	locals := cfg.Backends.Local
	items := make([]DockerBackendRuntime, 0, len(cfg.Backends.Docker))
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
		items = append(items, rt)
	}
	rtReg.Reconfigure(items)
}

// BackendByName returns runtime config for a named Docker backend (empty name matches first entry).
func BackendByName(name string) (DockerBackendRuntime, bool) {
	return rtReg.ByName(name)
}
