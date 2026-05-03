package cli

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func buildProviders(cfg *config.File) []hosts.Backend {
	return searchrun.BuildProviders(cfg, providerFlagsSnapshot())
}
