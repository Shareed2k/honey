package truenasprovider

import "github.com/spf13/cobra"

var cliFlags struct {
	url      string
	user     string
	apiKey   string
	insecure bool
}

// RegisterFlags adds TrueNAS CLI flags to cmd.
func RegisterFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliFlags.url, "truenas-url", "", "TrueNAS SCALE URL (https://host or wss://host/api/current)")
	cmd.Flags().StringVar(&cliFlags.user, "truenas-user", "", "TrueNAS API key username (default root)")
	cmd.Flags().StringVar(&cliFlags.apiKey, "truenas-api-key", "", "TrueNAS API key (or TRUENAS_API_KEY)")
	cmd.Flags().BoolVar(&cliFlags.insecure, "truenas-insecure", false, "Skip TLS verification for TrueNAS")
}
