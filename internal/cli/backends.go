package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
)

var (
	flagBackendsJSON bool
)

var backendsCmd = &cobra.Command{
	Use:   "backends",
	Short: "List backends defined in the honey config file",
	Long: `Resolves the config file the same way as search (--config, HONEY_CONFIG, or default paths),
	and lists all backends with a "name" property across all providers.`,
	Args: cobra.NoArgs,
	RunE: runBackends,
}

func init() {
	rootCmd.AddCommand(backendsCmd)
	backendsCmd.Flags().BoolVar(&flagBackendsJSON, "json", false, "Print JSON (config_path + backends) instead of a table")
}

func runBackends(cmd *cobra.Command, _ []string) error {
	cfgPath := resolvedCfgPath
	if cfgPath == "" {
		return fmt.Errorf("no config file found; run 'honey config' to create one")
	}
	cfg := resolvedCfg
	rows := searchrun.ListBackendRows(cfg)
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
