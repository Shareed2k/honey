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

func (localFactory) FromConfig(cfg *config.File, _ searchrun.ProviderFlags) []hosts.Backend {
	if cfg == nil || len(cfg.Backends.Local) == 0 {
		return nil
	}
	var out []hosts.Backend
	for _, b := range cfg.Backends.Local {
		bk := searchrun.WithDockerDiscover(
			&Local{
				Name:  b.Name,
				Hosts: b.Hosts,
			},
			searchrun.MergeDockerDiscover(cfg.Defaults.DockerDiscover, b.DockerDiscover),
		)
		out = append(out, bk)
	}
	return out
}

func (localFactory) Default(_ searchrun.ProviderFlags) hosts.Backend {
	return searchrun.WithDockerDiscover(
		&Local{},
		config.DockerDiscover{},
	)
}

func (localFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.Local))
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

var (
	_ searchrun.ProviderFactory       = localFactory{}
	_ searchrun.BackendConfigRegistry = localFactory{}
)
