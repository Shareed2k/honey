package consulprovider

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

const overrideKey = "consul"

func consulOverride(overrides searchrun.ProviderOverrides) (o config.ConsulBackend) {
	json.Unmarshal(overrides[overrideKey], &o) //nolint:errcheck
	return o
}

// NewFactory returns a new factory for this provider.
func NewFactory() searchrun.ProviderFactory {
	return consulFactory{}
}

type consulFactory struct{}

func (consulFactory) FromConfig(overrides searchrun.ProviderOverrides) []hosts.Backend {
	cfg := config.Get()
	o := consulOverride(overrides)
	out := make([]hosts.Backend, 0, len(cfg.Backends.Consul))
	for _, e := range cfg.Backends.Consul {
		addr := searchrun.FirstNonEmpty(e.Addr, o.Addr, cliFlags.addr)
		dc := searchrun.FirstNonEmpty(e.Datacenter, o.Datacenter, cliFlags.datacenter)
		tok := searchrun.FirstNonEmpty(e.Token, o.Token, cliFlags.token)
		b := searchrun.WithDockerDiscover(
			&Consul{Name: e.Name, Addr: addr, Datacenter: dc, Token: tok},
			searchrun.MergeDockerDiscover(cfg.Defaults.DockerDiscover, e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (consulFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
	o := consulOverride(overrides)
	addr := searchrun.FirstNonEmpty(o.Addr, cliFlags.addr)
	dc := searchrun.FirstNonEmpty(o.Datacenter, cliFlags.datacenter)
	tok := searchrun.FirstNonEmpty(o.Token, cliFlags.token)
	return searchrun.WithDockerDiscover(
		&Consul{Addr: addr, Datacenter: dc, Token: tok},
		config.DockerDiscover{},
	)
}

func (consulFactory) BackendRows() []config.BackendRow {
	cfg := config.Get()
	rows := make([]config.BackendRow, 0, len(cfg.Backends.Consul))
	for _, e := range cfg.Backends.Consul {
		rows = append(rows, config.BackendRow{Kind: "consul", Name: e.Name, Hint: strings.TrimSpace(e.Addr)})
	}
	return rows
}

func (consulFactory) BackendKind() string { return "consul" }

func (consulFactory) BackendSlicePtr() any {
	cfg := config.Get()
	return &cfg.Backends.Consul
}

func (consulFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }
