package cli

import (
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, commit, date, and logo",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		PrintVersion(cmd.OutOrStdout())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
