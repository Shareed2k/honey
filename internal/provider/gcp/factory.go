package gcp

import (
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.Register(gcpFactory{})
}

type gcpFactory struct{}

func (gcpFactory) FromConfig(cfg *config.File, f searchrun.ProviderFlags) []hosts.Backend {
	out := make([]hosts.Backend, 0, len(cfg.Backends.GCP))
	for _, e := range cfg.Backends.GCP {
		proj, zone := e.Project, e.Zone
		if proj == "" {
			proj = f.GCPProject
		}
		if zone == "" {
			zone = f.GCPZone
		}
		b := searchrun.WithDockerDiscover(
			&GCP{Name: e.Name, Project: proj, Zone: zone},
			searchrun.MergeDockerDiscover(cfg.Defaults.DockerDiscover, e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (gcpFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	return searchrun.WithDockerDiscover(
		&GCP{Project: f.GCPProject, Zone: f.GCPZone},
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
