package proxmoxprovider

import "github.com/spf13/cobra"

var cliFlags struct {
	url         string
	user        string
	password    string
	tokenID     string
	tokenSecret string
	insecure    bool
}

// RegisterFlags adds Proxmox CLI flags to cmd.
func RegisterFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliFlags.url, "proxmox-url", "", "Proxmox API URL (e.g. https://10.0.0.1:8006/api2/json)")
	cmd.Flags().StringVar(&cliFlags.user, "proxmox-user", "", "Proxmox user (e.g. root@pam)")
	cmd.Flags().StringVar(&cliFlags.password, "proxmox-password", "", "Proxmox password")
	cmd.Flags().StringVar(&cliFlags.tokenID, "proxmox-token-id", "", "Proxmox token ID (e.g. root@pam!token)")
	cmd.Flags().StringVar(&cliFlags.tokenSecret, "proxmox-token-secret", "", "Proxmox token secret")
	cmd.Flags().BoolVar(&cliFlags.insecure, "proxmox-insecure", false, "Skip TLS verification for Proxmox")
}
