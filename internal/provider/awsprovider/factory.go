package awsprovider

import (
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.Register(awsFactory{})
}

type awsFactory struct{}

func (awsFactory) FromConfig(cfg *config.File, f searchrun.ProviderFlags) []hosts.Backend {
	out := make([]hosts.Backend, 0, len(cfg.Backends.AWS))
	for _, e := range cfg.Backends.AWS {
		prof, reg := e.Profile, e.Region
		if prof == "" {
			prof = f.AWSProfile
		}
		if reg == "" {
			reg = f.AWSRegion
		}
		b := searchrun.WithDockerDiscover(
			&AWS{Name: e.Name, Profile: prof, Region: reg},
			searchrun.MergeDockerDiscover(cfg.Defaults.DockerDiscover, e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (awsFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	return searchrun.WithDockerDiscover(
		&AWS{Profile: f.AWSProfile, Region: f.AWSRegion},
		config.DockerDiscover{},
	)
}

func (awsFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.AWS))
	for _, e := range cfg.Backends.AWS {
		rows = append(rows, config.BackendRow{Kind: "aws", Name: e.Name, Hint: strings.TrimSpace(e.Profile)})
	}
	return rows
}

func (awsFactory) BackendKind() string { return "aws" }

func (awsFactory) BackendSlicePtr(cfg *config.File) any { return &cfg.Backends.AWS }
