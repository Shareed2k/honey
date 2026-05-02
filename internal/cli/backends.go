package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
)

var (
	flagBackendsConfig string
	flagBackendsJSON   bool
)

var backendsCmd = &cobra.Command{
	Use:   "backends",
	Short: "List backends defined in the honey config file",
	Long: `Resolves the config file the same way as search (--config, HONEY_CONFIG, HOSTCTL_CONFIG, or default paths),
then prints each backends.* entry: kind, optional name, and a short hint (project, profile/region, kube context, consul addr).

Exits with an error if no config file is found. If the file has no backends: lists,
honey search uses implicit providers from flags only.`,
	Args: cobra.NoArgs,
	RunE: runBackends,
}

func init() {
	rootCmd.AddCommand(backendsCmd)
	backendsCmd.Flags().StringVar(&flagBackendsConfig, "config", "", "Path to honey YAML (optional; also HONEY_CONFIG / HOSTCTL_CONFIG or default paths)")
	backendsCmd.Flags().BoolVar(&flagBackendsJSON, "json", false, "Print JSON (config_path + backends) instead of a table")
}

func runBackends(cmd *cobra.Command, _ []string) error {
	cfgPath, err := config.ResolvePath(flagBackendsConfig)
	if err != nil {
		return err
	}
	if cfgPath == "" {
		return fmt.Errorf("no config file found (pass --config, set HONEY_CONFIG or HOSTCTL_CONFIG, or add ~/.config/honey/config.yaml)")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	rows := cfg.ListBackendRows()
	if flagBackendsJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			ConfigPath string              `json:"config_path"`
			Backends   []config.BackendRow `json:"backends"`
		}{ConfigPath: cfgPath, Backends: rows})
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "# %s\n", cfgPath)
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "(no backends: entries; search uses flag-only implicit providers)")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "KIND\tNAME\tHINT")
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", r.Kind, r.Name, r.Hint)
	}
	return w.Flush()
}
