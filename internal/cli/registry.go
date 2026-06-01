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
		globalSearchRegistry = searchrun.NewRegistry(all.Factories())
	}
	return globalSearchRegistry
}

// buildHostExecRegistry constructs the host execution registry with all necessary dependencies.
func buildHostExecRegistry() hostexec.Registry {
	searchReg := getSearchRegistry()
	return &hostexec.StandardRegistry{
		Resolver:       searchReg.ResolveExecutor,
		Reloader:       searchReg.ReconfigureFromConfig,
		Dialer:         sshclient.DialHoneyHost,
		RunInteractive: ui.RunSSHInteractive,
		RunTunnel:      sshclient.RunTunnelGo,
	}
}
