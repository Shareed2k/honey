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

// ConfigProvider defines the configuration dependency required by this provider.
type ConfigProvider interface {
	DockerBackends() []config.DockerBackend
	DockerBackendSlicePtr() *[]config.DockerBackend
	SetDockerBackends([]config.DockerBackend)
	LocalBackends() []config.LocalBackend
}

const overrideKey = "docker"

func dockerOverride(overrides searchrun.ProviderOverrides) (o config.DockerBackend) {
	if len(overrides[overrideKey]) > 0 {
		_ = json.Unmarshal(overrides[overrideKey], &o) // overrides are optional
	}
	return o
}

// NewFactory returns a new factory for this provider. interactive (implemented in
// the ui package) is injected so resolver-created executors can run TTY sessions.
func NewFactory(interactive InteractiveRunner, cfg ConfigProvider) searchrun.ProviderFactory {
	searchrun.RegisterDockerDiscover(DiscoverOnVMs)
	return dockerFactory{interactive: interactive, cfg: cfg}
}

type dockerFactory struct {
	interactive InteractiveRunner
	cfg         ConfigProvider
}

func (f dockerFactory) FromConfig(overrides searchrun.ProviderOverrides) []hosts.Backend {
	locals := f.cfg.LocalBackends()
	out := make([]hosts.Backend, 0, len(f.cfg.DockerBackends()))
	for _, e := range f.cfg.DockerBackends() {
		bc := BackendConfigFromYAML(e, locals, "")
		applyDockerFlags(&bc, overrides)
		out = append(out, &Docker{Config: bc})
	}
	return out
}

func (f dockerFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
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

func (f dockerFactory) BackendRows() []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(f.cfg.DockerBackends()))
	for _, e := range f.cfg.DockerBackends() {
		hint := strings.TrimSpace(e.Host)
		if hint == "" && strings.TrimSpace(e.ViaLocal) != "" {
			hint = "via_local:" + strings.TrimSpace(e.ViaLocal)
		}
		rows = append(rows, config.BackendRow{Kind: "docker", Name: e.Name, Hint: hint})
	}
	return rows
}

func (f dockerFactory) BackendKind() string { return "docker" }

func (f dockerFactory) BackendSlicePtr() any {
	return f.cfg.DockerBackendSlicePtr()
}

func (f dockerFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }

func (f dockerFactory) ProviderName() string { return "docker" }

func (f dockerFactory) ExecutorFor(r hosts.Record, reg hostexec.Registry) hostexec.Executor {
	k := strings.ToLower(strings.TrimSpace(r.Meta["kind"]))
	if k == "container" || k == "swarm_task" {
		return &DockerExecutor{reg: reg, interactive: f.interactive}
	}
	return nil
}

func (f dockerFactory) ReconfigureFromConfig() {
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
