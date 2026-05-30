package dockerprovider

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
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
	host := strings.TrimSpace(f.DockerHost)
	if host == "" {
		host = strings.TrimSpace(cliFlags.host)
	}
	viaLocal := strings.TrimSpace(f.DockerViaLocal)
	if viaLocal == "" {
		viaLocal = strings.TrimSpace(cliFlags.viaLocal)
	}
	socket := strings.TrimSpace(f.DockerSocket)
	if socket == "" {
		socket = strings.TrimSpace(cliFlags.socket)
	}
	platform := strings.TrimSpace(f.DockerPlatform)
	if platform == "" {
		platform = strings.TrimSpace(cliFlags.platform)
	}
	mode := strings.TrimSpace(f.DockerMode)
	if mode == "" {
		mode = strings.TrimSpace(cliFlags.mode)
	}
	allContainers := f.DockerAllContainers || cliFlags.allContainers
	bc := BackendConfig{
		Host:          host,
		ViaLocal:      viaLocal,
		Socket:        socket,
		Platform:      platform,
		Mode:          mode,
		AllContainers: allContainers,
	}
	viaSSHHost := strings.TrimSpace(f.DockerViaSSHHost)
	if viaSSHHost == "" {
		viaSSHHost = strings.TrimSpace(cliFlags.viaSSHHost)
	}
	if viaSSHHost != "" {
		bc.ViaSSH.Host = viaSSHHost
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

func (dockerFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }

func (dockerFactory) ProviderName() string { return "docker" }

func (dockerFactory) ExecutorFor(r hosts.Record) hostexec.Executor {
	k := strings.ToLower(strings.TrimSpace(r.Meta["kind"]))
	if k == "container" || k == "swarm_task" {
		return DockerExecutor{}
	}
	return nil
}

func (dockerFactory) ReconfigureFromConfig(cfg *config.File) { reconfigureDocker(cfg) }

func applyDockerFlags(bc *BackendConfig, f searchrun.ProviderFlags) {
	if v := strings.TrimSpace(f.DockerHost); v != "" {
		bc.Host = v
	} else if v := strings.TrimSpace(cliFlags.host); v != "" {
		bc.Host = v
	}
	if v := strings.TrimSpace(f.DockerViaLocal); v != "" {
		bc.ViaLocal = v
	} else if v := strings.TrimSpace(cliFlags.viaLocal); v != "" {
		bc.ViaLocal = v
	}
	if v := strings.TrimSpace(f.DockerViaSSHHost); v != "" {
		bc.ViaSSH.Host = v
	} else if v := strings.TrimSpace(cliFlags.viaSSHHost); v != "" {
		bc.ViaSSH.Host = v
	}
	if v := strings.TrimSpace(f.DockerSocket); v != "" {
		bc.Socket = v
	} else if v := strings.TrimSpace(cliFlags.socket); v != "" {
		bc.Socket = v
	}
	if v := strings.TrimSpace(f.DockerPlatform); v != "" {
		bc.Platform = v
	} else if v := strings.TrimSpace(cliFlags.platform); v != "" {
		bc.Platform = v
	}
	if v := strings.TrimSpace(f.DockerMode); v != "" {
		bc.Mode = v
	} else if v := strings.TrimSpace(cliFlags.mode); v != "" {
		bc.Mode = v
	}
	if f.DockerAllContainers || cliFlags.allContainers {
		bc.AllContainers = true
	}
}
