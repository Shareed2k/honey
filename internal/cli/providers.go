package cli

import (
	"hostctl/internal/config"
	"hostctl/internal/hosts"
	"hostctl/internal/searchrun"
)

func buildProviders(cfg *config.File) []hosts.Backend {
	return searchrun.BuildProviders(cfg, providerFlagsSnapshot())
}
