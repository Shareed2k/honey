package gcp

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
	GCPBackends() []config.GCPBackend
	GCPBackendSlicePtr() *[]config.GCPBackend
	SetGCPBackends([]config.GCPBackend)
	DockerDiscover() config.DockerDiscover
}

const overrideKey = "gcp"

func gcpOverride(overrides searchrun.ProviderOverrides) (o config.GCPBackend) {
	json.Unmarshal(overrides[overrideKey], &o) //nolint:errcheck
	return o
}

// NewFactory returns a new factory for this provider.
func NewFactory(cfg ConfigProvider) searchrun.ProviderFactory {
	searchrun.RegisterCRUD(gcpCRUD{cfg: cfg})
	return gcpFactory{cfg: cfg}
}

type gcpFactory struct {
	cfg ConfigProvider
}

func (f gcpFactory) FromConfig(overrides searchrun.ProviderOverrides) []hosts.Backend {
	o := gcpOverride(overrides)
	out := make([]hosts.Backend, 0, len(f.cfg.GCPBackends()))
	for _, e := range f.cfg.GCPBackends() {
		proj := searchrun.FirstNonEmpty(e.Project, o.Project, cliFlags.project)
		zone := searchrun.FirstNonEmpty(e.Zone, o.Zone, cliFlags.zone)
		b := searchrun.WithDockerDiscover(
			&GCP{Name: e.Name, Project: proj, Zone: zone},
			searchrun.MergeDockerDiscover(f.cfg.DockerDiscover(), e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (f gcpFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
	o := gcpOverride(overrides)
	proj := searchrun.FirstNonEmpty(o.Project, cliFlags.project)
	zone := searchrun.FirstNonEmpty(o.Zone, cliFlags.zone)
	return searchrun.WithDockerDiscover(
		&GCP{Project: proj, Zone: zone},
		config.DockerDiscover{}, // no defaults available in Default() since cfg is nil
	)
}

func (f gcpFactory) BackendRows() []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(f.cfg.GCPBackends()))
	for _, e := range f.cfg.GCPBackends() {
		rows = append(rows, config.BackendRow{Kind: "gcp", Name: e.Name, Hint: strings.TrimSpace(e.Project)})
	}
	return rows
}

func (f gcpFactory) BackendKind() string { return "gcp" }

func (f gcpFactory) BackendSlicePtr() any {
	return f.cfg.GCPBackendSlicePtr()
}

func (f gcpFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }
