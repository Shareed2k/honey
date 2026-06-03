package awsprovider

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

const overrideKey = "aws"

func awsOverride(overrides searchrun.ProviderOverrides) (o config.AWSBackend) {
	json.Unmarshal(overrides[overrideKey], &o) //nolint:errcheck
	return o
}

// NewFactory returns a new factory for this provider.
func NewFactory() searchrun.ProviderFactory {
	return awsFactory{}
}

type awsFactory struct{}

func (awsFactory) FromConfig(cfg *config.File, overrides searchrun.ProviderOverrides) []hosts.Backend {
	o := awsOverride(overrides)
	out := make([]hosts.Backend, 0, len(cfg.Backends.AWS))
	for _, e := range cfg.Backends.AWS {
		prof := searchrun.FirstNonEmpty(e.Profile, o.Profile, cliFlags.profile)
		reg := searchrun.FirstNonEmpty(e.Region, o.Region, cliFlags.region)
		b := searchrun.WithDockerDiscover(
			&AWS{Name: e.Name, Profile: prof, Region: reg},
			searchrun.MergeDockerDiscover(cfg.Defaults.DockerDiscover, e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (awsFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
	o := awsOverride(overrides)
	prof := searchrun.FirstNonEmpty(o.Profile, cliFlags.profile)
	reg := searchrun.FirstNonEmpty(o.Region, cliFlags.region)
	return searchrun.WithDockerDiscover(
		&AWS{Profile: prof, Region: reg},
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

func (awsFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }
