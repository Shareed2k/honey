package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/plugins"
)

var pluginsCmd = &cobra.Command{
	Use:   "plugins",
	Short: "List loaded WASM plugins",
}

var pluginsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show plugin id, capabilities, and path",
	RunE:  runPluginsList,
}

func init() {
	rootCmd.AddCommand(pluginsCmd)
	pluginsCmd.AddCommand(pluginsListCmd)
}

func runPluginsList(cmd *cobra.Command, _ []string) error {
	cfgPath, err := config.ResolvePath(flagConfig)
	if err != nil {
		return err
	}
	var cfg *config.File
	if cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			return err
		}
	}
	mgr, err := plugins.Open(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer func() { _ = mgr.Close() }()
	list := mgr.List()
	if !mgr.Enabled() {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "plugins: disabled (set plugins.enabled: true in honey config)")
	}
	if len(list) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no plugins loaded")
		return nil
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}
