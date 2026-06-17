package dockerprovider

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

const overrideKey = "docker"

func dockerOverride(overrides searchrun.ProviderOverrides) (o config.DockerBackend) {
	json.Unmarshal(overrides[overrideKey], &o) //nolint:errcheck
	return o
}

// NewFactory returns a new factory for this provider. interactive (implemented in
// the ui package) is injected so resolver-created executors can run TTY sessions.
func NewFactory(interactive InteractiveRunner) searchrun.ProviderFactory {
	searchrun.RegisterDockerDiscover(DiscoverOnVMs)
	return dockerFactory{interactive: interactive}
}

type dockerFactory struct {
	interactive InteractiveRunner
}

func (dockerFactory) FromConfig(overrides searchrun.ProviderOverrides) []hosts.Backend {
	cfg := config.Get()
	locals := cfg.Backends.Local
	out := make([]hosts.Backend, 0, len(cfg.Backends.Docker))
	for _, e := range cfg.Backends.Docker {
		bc := BackendConfigFromYAML(e, locals, "")
		applyDockerFlags(&bc, overrides)
		out = append(out, &Docker{Config: bc})
	}
	return out
}

func (dockerFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
	o := dockerOverride(overrides)
	host := strings.TrimSpace(searchrun.FirstNonEmpty(o.Host, cliFlags.host))
	viaLocal := strings.TrimSpace(searchrun.FirstNonEmpty(o.ViaLocal, cliFlags.viaLocal))
	socket := strings.TrimSpace(searchrun.FirstNonEmpty(o.Socket, cliFlags.socket))
	platform := strings.TrimSpace(searchrun.FirstNonEmpty(o.Platform, cliFlags.platform))
	mode := strings.TrimSpace(searchrun.FirstNonEmpty(o.Mode, cliFlags.mode))
	allContainers := o.AllContainers || cliFlags.allContainers
	bc := BackendConfig{
		Host:          host,
		ViaLocal:      viaLocal,
		Socket:        socket,
		Platform:      platform,
		Mode:          mode,
		AllContainers: allContainers,
	}
	viaSSHHost := strings.TrimSpace(searchrun.FirstNonEmpty(o.ViaSSH.Host, cliFlags.viaSSHHost))
	if viaSSHHost != "" {
		bc.ViaSSH.Host = viaSSHHost
	}
	return &Docker{Config: bc}
}

func (dockerFactory) BackendRows() []config.BackendRow {
	cfg := config.Get()
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

func (dockerFactory) BackendSlicePtr() any {
	cfg := config.Get()
	return &cfg.Backends.Docker
}

func (dockerFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }

func (dockerFactory) ProviderName() string { return "docker" }

func (f dockerFactory) ExecutorFor(r hosts.Record, reg hostexec.Registry) hostexec.Executor {
	k := strings.ToLower(strings.TrimSpace(r.Meta["kind"]))
	if k == "container" || k == "swarm_task" {
		return &DockerExecutor{reg: reg, interactive: f.interactive}
	}
	return nil
}

func (dockerFactory) ReconfigureFromConfig() {
	reconfigureDocker()
}

func applyDockerFlags(bc *BackendConfig, overrides searchrun.ProviderOverrides) {
	o := dockerOverride(overrides)
	if v := strings.TrimSpace(searchrun.FirstNonEmpty(o.Host, cliFlags.host)); v != "" {
		bc.Host = v
	}
	if v := strings.TrimSpace(searchrun.FirstNonEmpty(o.ViaLocal, cliFlags.viaLocal)); v != "" {
		bc.ViaLocal = v
	}
	if v := strings.TrimSpace(searchrun.FirstNonEmpty(o.ViaSSH.Host, cliFlags.viaSSHHost)); v != "" {
		bc.ViaSSH.Host = v
	}
	if v := strings.TrimSpace(searchrun.FirstNonEmpty(o.Socket, cliFlags.socket)); v != "" {
		bc.Socket = v
	}
	if v := strings.TrimSpace(searchrun.FirstNonEmpty(o.Platform, cliFlags.platform)); v != "" {
		bc.Platform = v
	}
	if v := strings.TrimSpace(searchrun.FirstNonEmpty(o.Mode, cliFlags.mode)); v != "" {
		bc.Mode = v
	}
	if o.AllContainers || cliFlags.allContainers {
		bc.AllContainers = true
	}
}
