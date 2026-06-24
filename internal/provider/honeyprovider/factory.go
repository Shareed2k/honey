package honeyprovider

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// ConfigProvider defines the configuration dependency required by this provider.
type ConfigProvider interface {
	HoneyBackends() []config.HoneyBackend
	HoneyBackendSlicePtr() *[]config.HoneyBackend
	SetHoneyBackends([]config.HoneyBackend)
}

// NewFactory returns a new factory for this provider.
func NewFactory(cfg ConfigProvider) searchrun.ProviderFactory {
	searchrun.RegisterCRUD(honeyCRUD{cfg: cfg})
	return honeyFactory{cfg: cfg}
}

type honeyFactory struct {
	cfg ConfigProvider
}

func (f honeyFactory) FromConfig(_ searchrun.ProviderOverrides) []hosts.Backend {
	if len(f.cfg.HoneyBackends()) == 0 {
		return nil
	}
	var out []hosts.Backend
	for _, b := range f.cfg.HoneyBackends() {
		out = append(out, &Honey{
			Name:     b.Name,
			URL:      b.URL,
			Token:    b.Token,
			Insecure: b.Insecure,
		})
	}
	return out
}

func (f honeyFactory) Default(_ searchrun.ProviderOverrides) hosts.Backend {
	return &Honey{}
}

func (f honeyFactory) BackendRows() []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(f.cfg.HoneyBackends()))
	for _, b := range f.cfg.HoneyBackends() {
		rows = append(rows, config.BackendRow{
			Kind: "honey",
			Name: b.Name,
			Hint: b.URL,
		})
	}
	return rows
}

func (f honeyFactory) BackendKind() string {
	return "honey"
}

func (f honeyFactory) BackendSlicePtr() any {
	return f.cfg.HoneyBackendSlicePtr()
}

var (
	_ searchrun.ProviderFactory       = honeyFactory{}
	_ searchrun.BackendConfigRegistry = honeyFactory{}
)
