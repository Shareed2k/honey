package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/plugins"
)

var pluginsCmd = &cobra.Command{
	Use:   "plugins",
	Short: "Manage WASM plugins",
}

var pluginsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show plugin id, capabilities, and path",
	RunE:  runPluginsList,
}

var (
	pluginsInstallForce bool
	pluginsInstallDir   string
)

var pluginsInstallCmd = &cobra.Command{
	Use:   "install <src>",
	Short: "Install a plugin from a URL, archive, or local directory",
	Long: `Install a WASM plugin into the configured plugins directory.

<src> may be:
  - An https:// URL to a .tar.gz or .zip archive
  - A local .tar.gz or .zip file
  - A local directory containing plugin.yaml and plugin.wasm

The plugin is installed to <plugins-dir>/<plugin-id>/.
`,
	Args: cobra.ExactArgs(1),
	RunE: runPluginsInstall,
}

func init() {
	rootCmd.AddCommand(pluginsCmd)
	pluginsCmd.AddCommand(pluginsListCmd)
	pluginsCmd.AddCommand(pluginsInstallCmd)

	pluginsInstallCmd.Flags().BoolVarP(&pluginsInstallForce, "force", "f", false, "Overwrite existing plugin")
	pluginsInstallCmd.Flags().StringVar(&pluginsInstallDir, "dir", "", "Override plugins directory (default: from config or ~/.config/honey/plugins)")
}

func runPluginsList(cmd *cobra.Command, _ []string) error {
	mgr, err := plugins.Open(context.Background(), resolvedCfg)
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

func runPluginsInstall(cmd *cobra.Command, args []string) error {
	src := args[0]

	// Resolve plugins directory: --dir flag → config plugins.directory → default
	dir := strings.TrimSpace(pluginsInstallDir)
	if dir == "" {
		if resolvedCfg != nil && strings.TrimSpace(resolvedCfg.Plugins.Directory) != "" {
			dir = strings.TrimSpace(resolvedCfg.Plugins.Directory)
		} else {
			dir = config.DefaultPluginsDir()
		}
	}

	m, err := plugins.Install(cmd.Context(), src, dir, pluginsInstallForce)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Installed plugin %s v%s → %s\n", m.ID, m.Version, dir+"/"+m.ID+"/")
	if len(m.Capabilities) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Capabilities: %s\n", strings.Join(m.Capabilities, ", "))
	}
	return nil
}
