package localprovider

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// ConfigProvider defines the configuration dependency required by this provider.
type ConfigProvider interface {
	LocalBackends() []config.LocalBackend
	LocalBackendSlicePtr() *[]config.LocalBackend
	SetLocalBackends([]config.LocalBackend)
	DockerDiscover() config.DockerDiscover
}

// NewFactory returns a new factory for this provider.
func NewFactory(cfg ConfigProvider) searchrun.ProviderFactory {
	searchrun.RegisterCRUD(localCRUD{cfg: cfg})
	return localFactory{cfg: cfg}
}

type localFactory struct {
	cfg ConfigProvider
}

func (f localFactory) FromConfig(_ searchrun.ProviderOverrides) []hosts.Backend {
	if len(f.cfg.LocalBackends()) == 0 {
		return nil
	}
	var out []hosts.Backend
	for _, b := range f.cfg.LocalBackends() {
		bk := searchrun.WithDockerDiscover(
			&Local{
				Name:  b.Name,
				Hosts: b.Hosts,
			},
			searchrun.MergeDockerDiscover(f.cfg.DockerDiscover(), b.DockerDiscover),
		)
		out = append(out, bk)
	}
	return out
}

func (f localFactory) Default(_ searchrun.ProviderOverrides) hosts.Backend {
	return searchrun.WithDockerDiscover(
		&Local{},
		config.DockerDiscover{},
	)
}

func (f localFactory) BackendRows() []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(f.cfg.LocalBackends()))
	for _, b := range f.cfg.LocalBackends() {
		rows = append(rows, config.BackendRow{
			Kind: "local",
			Name: b.Name,
			Hint: "",
		})
	}
	return rows
}

func (f localFactory) BackendKind() string {
	return "local"
}

func (f localFactory) BackendSlicePtr() any {
	return f.cfg.LocalBackendSlicePtr()
}

var (
	_ searchrun.ProviderFactory       = localFactory{}
	_ searchrun.BackendConfigRegistry = localFactory{}
)
