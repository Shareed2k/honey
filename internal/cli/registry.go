package cli

import (
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/all"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/sshclient"
)

var globalSearchRegistry *searchrun.Registry

func getSearchRegistry() *searchrun.Registry {
	if globalSearchRegistry == nil {
		globalSearchRegistry = searchrun.NewRegistry(all.Factories(all.Deps{
			K8sInteractive:    engine.K8sInteractiveRunner(),
			DockerInteractive: engine.DockerInteractiveRunner(),
			TruenasTunnel:     engine.TruenasTunnelRunner(),
			TruenasDialer:     engine.TruenasUpstreamDialer(),
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
		Interactive: hostexec.InteractiveRunnerFunc(func(user string, r hosts.Record, rec any) error {
			var sr *engine.SessionRecorder
			if rec != nil {
				sr, _ = rec.(*engine.SessionRecorder)
			}
			return engine.RunSSHInteractive(user, r, sr)
		}),
		Tunnel: hostexec.TunnelRunnerFunc(sshclient.RunTunnelGo),
	}
}
