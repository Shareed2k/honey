// Package cli implements the honey Cobra commands.
package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/logger"
	_ "github.com/shareed2k/honey/internal/provider/all" // register all providers natively during boot
)

var (
	flagDebugLog  string
	flagRecordDir string
)

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
	rootCmd.PersistentFlags().StringVar(&flagRecordDir, "record-dir", "", "Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default <directory of config.yaml>/records")

	rootCmd.AddCommand(searchCmd)
	rootCmd.SilenceUsage = true
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
}

// recordDirFlagChanged reports whether --record-dir was set on the command line
// (root persistent flag, visible to all subcommands).
func recordDirFlagChanged(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	f := cmd.Root().PersistentFlags().Lookup("record-dir")
	return f != nil && f.Changed
}
