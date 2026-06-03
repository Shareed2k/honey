package cli

import (
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/provider/all"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/ui"
)

var globalSearchRegistry *searchrun.Registry

func getSearchRegistry() *searchrun.Registry {
	if globalSearchRegistry == nil {
		globalSearchRegistry = searchrun.NewRegistry(all.Factories(all.Deps{
			K8sInteractive:    ui.K8sInteractiveRunner(),
			DockerInteractive: ui.DockerInteractiveRunner(),
			TruenasTunnel:     ui.TruenasTunnelRunner(),
			TruenasDialer:     ui.TruenasUpstreamDialer(),
		}))
	}
	return globalSearchRegistry
}

// buildHostExecRegistry constructs the host execution registry with all necessary dependencies.
func buildHostExecRegistry() hostexec.Registry {
	searchReg := getSearchRegistry()
	return &hostexec.StandardRegistry{
		Resolver:     searchReg, // *searchrun.Registry satisfies ExecutorResolver
		Reconfigurer: searchReg, // ...and Reconfigurer
		Dialer:       hostexec.DialerFunc(sshclient.DialHoneyHost),
		Interactive:  hostexec.InteractiveRunnerFunc(ui.RunSSHInteractive),
		Tunnel:       hostexec.TunnelRunnerFunc(sshclient.RunTunnelGo),
	}
}
