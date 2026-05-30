package awsprovider

import "github.com/spf13/cobra"

var cliFlags struct {
	profile string
	region  string
}

// RegisterFlags adds AWS CLI flags to cmd.
func RegisterFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliFlags.profile, "aws-profile", "", "AWS shared config profile")
	cmd.Flags().StringVar(&cliFlags.region, "aws-region", "", "AWS region (default: from profile/env)")
}
