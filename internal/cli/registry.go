package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/shareed2k/honey/internal/config"
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

// executionRouter implements hostexec.Registry using concrete functions.
type executionRouter struct {
	searchReg *searchrun.Registry
}

func (r *executionRouter) ForRecord(rec hosts.Record) hostexec.Executor {
	if ex := r.searchReg.ResolveExecutor(rec, r); ex != nil {
		return ex
	}
	return &sshFallbackExecutor{}
}

func (r *executionRouter) Reconfigure(cfg *config.File) {
	r.searchReg.ReconfigureFromConfig(cfg)
}

func (r *executionRouter) RunSSHTunnel(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error {
	return sshclient.RunTunnelGo(ctx, user, host, sshPort, localFwd, out)
}

func (r *executionRouter) BorrowSSH(_ string, _ hosts.Record) (any, bool) {
	return nil, false // Not implemented globally by default
}

type sshFallbackExecutor struct{}

func (e *sshFallbackExecutor) Dial(user string, r hosts.Record) (hostexec.HostClient, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
			user = u
		}
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return nil, fmt.Errorf("no host ip for ssh")
	}
	override := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		override = p
	}
	identity := ""
	if id, ok := hosts.MetaSSHIdentityFile(&r); ok {
		identity = id
	}
	return sshclient.DialHoneyHost(user, host, override, identity)
}

func (e *sshFallbackExecutor) RunInteractive(user string, r hosts.Record) error {
	user = strings.TrimSpace(user)
	if user == "" {
		if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
			user = u
		}
	}
	return engine.RunSSHInteractive(user, r, nil)
}

func (e *sshFallbackExecutor) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	user = strings.TrimSpace(user)
	if user == "" {
		if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
			user = u
		}
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return fmt.Errorf("no host ip for ssh")
	}
	override := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		override = p
	}
	return sshclient.RunTunnelGo(ctx, user, host, override, localFwd, out)
}

func (sshFallbackExecutor) DialUpstream(_ context.Context, _ string, _ hosts.Record, _ string) (net.Conn, error) {
	return nil, fmt.Errorf("sshFallbackExecutor.DialUpstream not implemented")
}

// buildHostExecRegistry constructs the host execution registry.
func buildHostExecRegistry() hostexec.Registry {
	return &executionRouter{searchReg: getSearchRegistry()}
}
