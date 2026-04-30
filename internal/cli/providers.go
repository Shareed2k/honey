package cli

import (
	"honey/internal/config"
	"honey/internal/hosts"
	"honey/internal/searchrun"
)

func buildProviders(cfg *config.File) []hosts.Backend {
	return searchrun.BuildProviders(cfg, providerFlagsSnapshot())
}
