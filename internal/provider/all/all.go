// Package all automatically invokes the init() registration for every native honey provider.
package all

import (
	// Register all providers
	_ "github.com/shareed2k/honey/internal/provider/awsprovider"
	_ "github.com/shareed2k/honey/internal/provider/consulprovider"
	_ "github.com/shareed2k/honey/internal/provider/gcp"
	_ "github.com/shareed2k/honey/internal/provider/k8sprovider"
	_ "github.com/shareed2k/honey/internal/provider/proxmoxprovider"
	_ "github.com/shareed2k/honey/internal/sshclient"
)
