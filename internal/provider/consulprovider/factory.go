package consulprovider

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// ConfigProvider defines the configuration dependency required by this provider.
type ConfigProvider interface {
	ConsulBackends() []config.ConsulBackend
	ConsulBackendSlicePtr() *[]config.ConsulBackend
	SetConsulBackends([]config.ConsulBackend)
	DockerDiscover() config.DockerDiscover
}

const overrideKey = "consul"

func consulOverride(overrides searchrun.ProviderOverrides) (o config.ConsulBackend) {
	if len(overrides[overrideKey]) > 0 {
		_ = json.Unmarshal(overrides[overrideKey], &o) // overrides are optional
	}
	return o
}

// NewFactory returns a new factory for this provider.
func NewFactory(cfg ConfigProvider) searchrun.ProviderFactory {
	searchrun.RegisterCRUD(consulCRUD{cfg: cfg})
	return consulFactory{cfg: cfg}
}

type consulFactory struct {
	cfg ConfigProvider
}

func (f consulFactory) FromConfig(overrides searchrun.ProviderOverrides) []hosts.Backend {
	o := consulOverride(overrides)
	out := make([]hosts.Backend, 0, len(f.cfg.ConsulBackends()))
	for _, e := range f.cfg.ConsulBackends() {
		addr := searchrun.FirstNonEmpty(e.Addr, o.Addr, cliFlags.addr)
		dc := searchrun.FirstNonEmpty(e.Datacenter, o.Datacenter, cliFlags.datacenter)
		tok := searchrun.FirstNonEmpty(e.Token, o.Token, cliFlags.token)
		b := searchrun.WithDockerDiscover(
			&Consul{Name: e.Name, Addr: addr, Datacenter: dc, Token: tok},
			searchrun.MergeDockerDiscover(f.cfg.DockerDiscover(), e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (f consulFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
	o := consulOverride(overrides)
	addr := searchrun.FirstNonEmpty(o.Addr, cliFlags.addr)
	dc := searchrun.FirstNonEmpty(o.Datacenter, cliFlags.datacenter)
	tok := searchrun.FirstNonEmpty(o.Token, cliFlags.token)
	return searchrun.WithDockerDiscover(
		&Consul{Addr: addr, Datacenter: dc, Token: tok},
		config.DockerDiscover{},
	)
}

func (f consulFactory) BackendRows() []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(f.cfg.ConsulBackends()))
	for _, e := range f.cfg.ConsulBackends() {
		rows = append(rows, config.BackendRow{Kind: "consul", Name: e.Name, Hint: strings.TrimSpace(e.Addr)})
	}
	return rows
}

func (f consulFactory) BackendKind() string { return "consul" }

func (f consulFactory) BackendSlicePtr() any {
	return f.cfg.ConsulBackendSlicePtr()
}

func (f consulFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }
