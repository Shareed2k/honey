package localprovider

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// NewFactory returns a new factory for this provider.
func NewFactory() searchrun.ProviderFactory {
	return localFactory{}
}

type localFactory struct{}

func (localFactory) FromConfig(_ searchrun.ProviderOverrides) []hosts.Backend {
	cfg := config.Get()
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

func (localFactory) Default(_ searchrun.ProviderOverrides) hosts.Backend {
	return searchrun.WithDockerDiscover(
		&Local{},
		config.DockerDiscover{},
	)
}

func (localFactory) BackendRows() []config.BackendRow {
	cfg := config.Get()
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

func (localFactory) BackendSlicePtr() any {
	cfg := config.Get()
	return &cfg.Backends.Local
}

var (
	_ searchrun.ProviderFactory       = localFactory{}
	_ searchrun.BackendConfigRegistry = localFactory{}
)
