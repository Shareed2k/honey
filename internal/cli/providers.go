package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func buildProviders(cfg *config.File, cmd *cobra.Command) []hosts.Backend {
	pf := searchrun.ProviderFlags{}
	if cfg != nil {
		if s := strings.TrimSpace(cfg.Defaults.K8sMode); s != "" && !cmd.Flags().Changed("k8s-mode") {
			pf.K8sMode = s
		}
		if s := strings.TrimSpace(cfg.Defaults.K8sDebugImage); s != "" && !cmd.Flags().Changed("k8s-debug-image") {
			pf.K8sDebugImage = s
		}
	}
	return searchrun.BuildProviders(cfg, pf)
}
