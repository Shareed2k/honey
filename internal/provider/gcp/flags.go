package gcp

import "github.com/spf13/cobra"

var cliFlags struct {
	project string
	zone    string
}

// RegisterFlags binds GCP CLI flags to cmd.
func RegisterFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliFlags.project, "gcp-project", "",
		"GCP project (or GOOGLE_CLOUD_PROJECT / GCP_PROJECT)")
	cmd.Flags().StringVar(&cliFlags.zone, "gcp-zone", "",
		"Limit GCP to a single zone (default: all zones)")
}
