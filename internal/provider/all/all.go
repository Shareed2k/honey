// Package all provides all native honey provider factories.
package all

import (
	"github.com/shareed2k/honey/internal/provider/awsprovider"
	"github.com/shareed2k/honey/internal/provider/consulprovider"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
	"github.com/shareed2k/honey/internal/provider/gcp"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
	"github.com/shareed2k/honey/internal/provider/localprovider"
	"github.com/shareed2k/honey/internal/provider/proxmoxprovider"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/searchrun"
	_ "github.com/shareed2k/honey/internal/sshclient" // Registers ssh defaults
)

// Factories returns a slice of all built-in provider factories.
func Factories() []searchrun.ProviderFactory {
	return []searchrun.ProviderFactory{
		awsprovider.NewFactory(),
		consulprovider.NewFactory(),
		dockerprovider.NewFactory(),
		gcp.NewFactory(),
		k8sprovider.NewFactory(),
		localprovider.NewFactory(),
		proxmoxprovider.NewFactory(),
		truenasprovider.NewFactory(),
	}
}
