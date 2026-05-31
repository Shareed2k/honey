package cli

import (
	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func buildProviders(cfg *config.File, _ *cobra.Command) []hosts.Backend {
	return searchrun.BuildProviders(cfg, nil)
}
