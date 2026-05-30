package consulprovider

import "github.com/spf13/cobra"

var cliFlags struct {
	addr       string
	datacenter string
	token      string
}

// RegisterFlags adds Consul CLI flags to cmd.
func RegisterFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliFlags.addr, "consul-addr", "", "Consul HTTP address (host:port, default CONSUL_HTTP_ADDR)")
	cmd.Flags().StringVar(&cliFlags.datacenter, "consul-datacenter", "", "Consul datacenter")
	cmd.Flags().StringVar(&cliFlags.token, "consul-token", "", "Consul ACL token (or CONSUL_HTTP_TOKEN)")
}
