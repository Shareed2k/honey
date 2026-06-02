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

// Deps carries the ui-implemented runners injected into the providers that need
// to call back into interactive/tunnel session handling. They are supplied by the
// composition root (internal/cli) to keep provider packages leaf-level.
type Deps struct {
	K8sInteractive    k8sprovider.InteractiveRunner
	DockerInteractive dockerprovider.InteractiveRunner
	TruenasTunnel     truenasprovider.TunnelRunner
	TruenasDialer     truenasprovider.UpstreamDialer
}

// Factories returns a slice of all built-in provider factories, wiring deps into
// the providers that need them.
func Factories(deps Deps) []searchrun.ProviderFactory {
	return []searchrun.ProviderFactory{
		awsprovider.NewFactory(),
		consulprovider.NewFactory(),
		dockerprovider.NewFactory(deps.DockerInteractive),
		gcp.NewFactory(),
		k8sprovider.NewFactory(deps.K8sInteractive),
		localprovider.NewFactory(),
		proxmoxprovider.NewFactory(),
		truenasprovider.NewFactory(deps.TruenasTunnel, deps.TruenasDialer),
	}
}
