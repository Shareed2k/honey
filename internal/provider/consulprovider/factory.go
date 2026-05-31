package consulprovider

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.Register(consulFactory{})
}

type consulFactory struct{}

func (consulFactory) FromConfig(cfg *config.File, f searchrun.ProviderFlags) []hosts.Backend {
	out := make([]hosts.Backend, 0, len(cfg.Backends.Consul))
	for _, e := range cfg.Backends.Consul {
		addr, dc, tok := e.Addr, e.Datacenter, e.Token
		if addr == "" {
			addr = f.ConsulAddr
		}
		if addr == "" {
			addr = cliFlags.addr
		}
		if dc == "" {
			dc = f.ConsulDatacenter
		}
		if dc == "" {
			dc = cliFlags.datacenter
		}
		if tok == "" {
			tok = f.ConsulToken
		}
		if tok == "" {
			tok = cliFlags.token
		}
		b := searchrun.WithDockerDiscover(
			&Consul{Name: e.Name, Addr: addr, Datacenter: dc, Token: tok},
			searchrun.MergeDockerDiscover(cfg.Defaults.DockerDiscover, e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (consulFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	addr := f.ConsulAddr
	if addr == "" {
		addr = cliFlags.addr
	}
	dc := f.ConsulDatacenter
	if dc == "" {
		dc = cliFlags.datacenter
	}
	tok := f.ConsulToken
	if tok == "" {
		tok = cliFlags.token
	}
	return searchrun.WithDockerDiscover(
		&Consul{Addr: addr, Datacenter: dc, Token: tok},
		config.DockerDiscover{},
	)
}

func (consulFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.Consul))
	for _, e := range cfg.Backends.Consul {
		rows = append(rows, config.BackendRow{Kind: "consul", Name: e.Name, Hint: strings.TrimSpace(e.Addr)})
	}
	return rows
}

func (consulFactory) BackendKind() string { return "consul" }

func (consulFactory) BackendSlicePtr(cfg *config.File) any { return &cfg.Backends.Consul }

func (consulFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }
