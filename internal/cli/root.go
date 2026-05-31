// Package cli implements the honey Cobra commands.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/logger"
	_ "github.com/shareed2k/honey/internal/provider/all" // register all providers natively during boot
	"github.com/shareed2k/honey/internal/searchrun"
)

var (
	flagDebugLog  string
	flagRecordDir string
	flagConfig    string

	// set once by PersistentPreRunE; safe to read from any subcommand RunE.
	resolvedCfgPath string
	resolvedCfg     *config.File
)

var rootCmd = &cobra.Command{
	Use:   "honey",
	Short: Tagline,
	Long:  "Search and operate on instances across GCP, AWS, Kubernetes, Consul, and Proxmox.",
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		if err := logger.Init(flagDebugLog); err != nil {
			return err
		}
		zap.L().Debug("Logger initialized", zap.String("args", strings.Join(os.Args, " ")))
		resolvedCfgPath, _ = config.ResolvePath(flagConfig)
		if resolvedCfgPath != "" {
			var loadErr error
			resolvedCfg, loadErr = config.Load(resolvedCfgPath)
			if loadErr != nil {
				return fmt.Errorf("config: %w", loadErr)
			}
		}
		applyCommandFlagDefaults(cmd, resolvedCfgPath)
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
	rootCmd.PersistentFlags().StringVar(&flagConfig, "config", "", "Path to honey YAML (optional; also HONEY_CONFIG or default paths)")
	rootCmd.PersistentFlags().StringVar(&flagRecordDir, "record-dir", "", "Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default <directory of config.yaml>/records")

	rootCmd.PersistentFlags().DurationVar(&flagCacheTTL, "cache-ttl", searchrun.DefaultCacheTTL, "Cache time-to-live (host discovery)")
	rootCmd.PersistentFlags().BoolVar(&flagNoCache, "no-cache", false, "Bypass read/write cache (host discovery)")
	rootCmd.PersistentFlags().BoolVar(&flagRefresh, "refresh", false, "Ignore cached entries and refresh (host discovery)")
	rootCmd.PersistentFlags().StringVar(&flagCacheDir, "cache-dir", "", "Override cache directory (default: XDG_CACHE_HOME/honey)")

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

// rootPersistentFlagChanged reports whether a root-level persistent flag was set on the command line.
func rootPersistentFlagChanged(cmd *cobra.Command, name string) bool {
	if cmd == nil {
		return false
	}
	f := cmd.Root().PersistentFlags().Lookup(name)
	return f != nil && f.Changed
}
