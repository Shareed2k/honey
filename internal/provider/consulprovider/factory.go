package consulprovider

import (
	"strings"

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
		if dc == "" {
			dc = f.ConsulDatacenter
		}
		if tok == "" {
			tok = f.ConsulToken
		}
		out = append(out, &Consul{Name: e.Name, Addr: addr, Datacenter: dc, Token: tok})
	}
	return out
}

func (consulFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	return &Consul{Addr: f.ConsulAddr, Datacenter: f.ConsulDatacenter, Token: f.ConsulToken}
}

func (consulFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.Consul))
	for _, e := range cfg.Backends.Consul {
		rows = append(rows, config.BackendRow{Kind: "consul", Name: e.Name, Hint: strings.TrimSpace(e.Addr)})
	}
	return rows
}
