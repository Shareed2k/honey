package localprovider

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.Register(localFactory{})
}

type localFactory struct{}

func (localFactory) FromConfig(cfg *config.File, f searchrun.ProviderFlags) []hosts.Backend {
	if cfg == nil || len(cfg.Backends.Local) == 0 {
		return nil
	}
	var out []hosts.Backend
	for _, b := range cfg.Backends.Local {
		out = append(out, &Local{
			Name:  b.Name,
			Hosts: b.Hosts,
		})
	}
	return out
}

func (localFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	return &Local{}
}

func (localFactory) BackendRows(cfg *config.File) []config.BackendRow {
	var rows []config.BackendRow
	for _, b := range cfg.Backends.Local {
		rows = append(rows, config.BackendRow{
			Kind: "local",
			Name: b.Name,
			Hint: "",
		})
	}
	return rows
}

func (localFactory) BackendKind() string {
	return "local"
}

func (localFactory) BackendSlicePtr(cfg *config.File) any {
	return &cfg.Backends.Local
}

var _ searchrun.ProviderFactory = localFactory{}
var _ searchrun.BackendConfigRegistry = localFactory{}
