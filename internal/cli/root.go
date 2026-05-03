// Package cli implements the honey Cobra commands.
package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/logger"
)

var flagDebugLog string

var rootCmd = &cobra.Command{
	Use:   "honey",
	Short: Tagline,
	Long:  "Search and operate on instances across GCP, AWS, Kubernetes, Consul, and Proxmox.",
	PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
		if err := logger.Init(flagDebugLog); err != nil {
			return err
		}
		zap.L().Debug("Logger initialized", zap.String("args", strings.Join(os.Args, " ")))
		return nil
	},
	PersistentPostRun: func(_ *cobra.Command, _ []string) {
		logger.Sync()
	},
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Prepend banner to usage (and to --help via UsageString inside default help template).
	defaultUsage := (&cobra.Command{}).UsageTemplate()
	rootCmd.SetUsageTemplate(BannerText() + "\n\n" + defaultUsage)

	rootCmd.PersistentFlags().StringVar(&flagDebugLog, "debug-log", "", "Path to write debug logs (disables debug logging if empty)")

	rootCmd.AddCommand(searchCmd)
	rootCmd.SilenceUsage = true
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
}
