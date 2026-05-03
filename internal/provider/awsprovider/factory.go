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
		out = append(out, &AWS{Name: e.Name, Profile: prof, Region: reg})
	}
	return out
}

func (awsFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	return &AWS{Profile: f.AWSProfile, Region: f.AWSRegion}
}

func (awsFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.AWS))
	for _, e := range cfg.Backends.AWS {
		rows = append(rows, config.BackendRow{Kind: "aws", Name: e.Name, Hint: strings.TrimSpace(e.Profile + " " + e.Region)})
	}
	return rows
}
