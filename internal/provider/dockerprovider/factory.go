package dockerprovider

import (
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.Register(dockerFactory{})
	searchrun.RegisterDockerDiscover(DiscoverOnVMs)
}

type dockerFactory struct{}

func (dockerFactory) FromConfig(cfg *config.File, f searchrun.ProviderFlags) []hosts.Backend {
	locals := cfg.Backends.Local
	out := make([]hosts.Backend, 0, len(cfg.Backends.Docker))
	for _, e := range cfg.Backends.Docker {
		bc := BackendConfigFromYAML(e, locals, "")
		applyDockerFlags(&bc, f)
		out = append(out, &Docker{Config: bc})
	}
	return out
}

func (dockerFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	bc := BackendConfig{
		Host:          strings.TrimSpace(f.DockerHost),
		ViaLocal:      strings.TrimSpace(f.DockerViaLocal),
		Socket:        strings.TrimSpace(f.DockerSocket),
		Platform:      strings.TrimSpace(f.DockerPlatform),
		Mode:          strings.TrimSpace(f.DockerMode),
		AllContainers: f.DockerAllContainers,
	}
	if h := strings.TrimSpace(f.DockerViaSSHHost); h != "" {
		bc.ViaSSH.Host = h
	}
	return &Docker{Config: bc}
}

func (dockerFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.Docker))
	for _, e := range cfg.Backends.Docker {
		hint := strings.TrimSpace(e.Host)
		if hint == "" && strings.TrimSpace(e.ViaLocal) != "" {
			hint = "via_local:" + strings.TrimSpace(e.ViaLocal)
		}
		rows = append(rows, config.BackendRow{Kind: "docker", Name: e.Name, Hint: hint})
	}
	return rows
}

func (dockerFactory) BackendKind() string { return "docker" }

func (dockerFactory) BackendSlicePtr(cfg *config.File) any { return &cfg.Backends.Docker }

func applyDockerFlags(bc *BackendConfig, f searchrun.ProviderFlags) {
	if strings.TrimSpace(f.DockerHost) != "" {
		bc.Host = strings.TrimSpace(f.DockerHost)
	}
	if strings.TrimSpace(f.DockerViaLocal) != "" {
		bc.ViaLocal = strings.TrimSpace(f.DockerViaLocal)
	}
	if strings.TrimSpace(f.DockerViaSSHHost) != "" {
		bc.ViaSSH.Host = strings.TrimSpace(f.DockerViaSSHHost)
	}
	if strings.TrimSpace(f.DockerSocket) != "" {
		bc.Socket = strings.TrimSpace(f.DockerSocket)
	}
	if strings.TrimSpace(f.DockerPlatform) != "" {
		bc.Platform = strings.TrimSpace(f.DockerPlatform)
	}
	if strings.TrimSpace(f.DockerMode) != "" {
		bc.Mode = strings.TrimSpace(f.DockerMode)
	}
	if f.DockerAllContainers {
		bc.AllContainers = true
	}
}
