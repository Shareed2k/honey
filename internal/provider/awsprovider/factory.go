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
func NewFactory(cfg ConfigProvider) searchrun.ProviderFactory {
	searchrun.RegisterCRUD(awsCRUD{cfg: cfg})
	return awsFactory{cfg: cfg}
}

// ConfigProvider defines the configuration dependency required by the AWS provider.
type ConfigProvider interface {
	AWSBackends() []config.AWSBackend
	AWSBackendSlicePtr() *[]config.AWSBackend
	SetAWSBackends([]config.AWSBackend)
	DockerDiscover() config.DockerDiscover
}

type awsFactory struct {
	cfg ConfigProvider
}

func (f awsFactory) FromConfig(overrides searchrun.ProviderOverrides) []hosts.Backend {
	o := awsOverride(overrides)
	out := make([]hosts.Backend, 0, len(f.cfg.AWSBackends()))
	for _, e := range f.cfg.AWSBackends() {
		prof := searchrun.FirstNonEmpty(e.Profile, o.Profile, cliFlags.profile)
		reg := searchrun.FirstNonEmpty(e.Region, o.Region, cliFlags.region)
		b := searchrun.WithDockerDiscover(
			&AWS{Name: e.Name, Profile: prof, Region: reg},
			searchrun.MergeDockerDiscover(f.cfg.DockerDiscover(), e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (f awsFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
	o := awsOverride(overrides)
	prof := searchrun.FirstNonEmpty(o.Profile, cliFlags.profile)
	reg := searchrun.FirstNonEmpty(o.Region, cliFlags.region)
	return searchrun.WithDockerDiscover(
		&AWS{Profile: prof, Region: reg},
		config.DockerDiscover{},
	)
}

func (f awsFactory) BackendRows() []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(f.cfg.AWSBackends()))
	for _, e := range f.cfg.AWSBackends() {
		rows = append(rows, config.BackendRow{Kind: "aws", Name: e.Name, Hint: strings.TrimSpace(e.Profile)})
	}
	return rows
}

func (f awsFactory) BackendKind() string { return "aws" }

func (f awsFactory) BackendSlicePtr() any {
	return f.cfg.AWSBackendSlicePtr()
}

func (awsFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }
