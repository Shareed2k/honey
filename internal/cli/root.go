// Package cli implements the honey Cobra commands.
package cli

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "honey",
	Short: Tagline,
	Long:  "Search and operate on instances across GCP, AWS, Kubernetes, and Consul.",
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Prepend banner to usage (and to --help via UsageString inside default help template).
	defaultUsage := (&cobra.Command{}).UsageTemplate()
	rootCmd.SetUsageTemplate(BannerText() + "\n\n" + defaultUsage)

	rootCmd.AddCommand(searchCmd)
	rootCmd.SilenceUsage = true
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
}
