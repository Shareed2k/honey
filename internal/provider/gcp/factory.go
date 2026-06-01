package gcp

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

const overrideKey = "gcp"

func gcpOverride(overrides searchrun.ProviderOverrides) (o config.GCPBackend) {
	json.Unmarshal(overrides[overrideKey], &o) //nolint:errcheck
	return o
}

func init() {
	searchrun.Register(gcpFactory{})
}

type gcpFactory struct{}

func (gcpFactory) FromConfig(cfg *config.File, overrides searchrun.ProviderOverrides) []hosts.Backend {
	o := gcpOverride(overrides)
	out := make([]hosts.Backend, 0, len(cfg.Backends.GCP))
	for _, e := range cfg.Backends.GCP {
		proj := searchrun.FirstNonEmpty(e.Project, o.Project, cliFlags.project)
		zone := searchrun.FirstNonEmpty(e.Zone, o.Zone, cliFlags.zone)
		b := searchrun.WithDockerDiscover(
			&GCP{Name: e.Name, Project: proj, Zone: zone},
			searchrun.MergeDockerDiscover(cfg.Defaults.DockerDiscover, e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (gcpFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
	o := gcpOverride(overrides)
	proj := searchrun.FirstNonEmpty(o.Project, cliFlags.project)
	zone := searchrun.FirstNonEmpty(o.Zone, cliFlags.zone)
	return searchrun.WithDockerDiscover(
		&GCP{Project: proj, Zone: zone},
		config.DockerDiscover{}, // no defaults available in Default() since cfg is nil
	)
}

func (gcpFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.GCP))
	for _, e := range cfg.Backends.GCP {
		rows = append(rows, config.BackendRow{Kind: "gcp", Name: e.Name, Hint: strings.TrimSpace(e.Project)})
	}
	return rows
}

func (gcpFactory) BackendKind() string { return "gcp" }

func (gcpFactory) BackendSlicePtr(cfg *config.File) any { return &cfg.Backends.GCP }

func (gcpFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }
