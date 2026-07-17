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
	"golang.org/x/crypto/ssh"
)

var globalSearchRegistry *searchrun.Registry

// GetSearchRegistry returns the global search registry, initializing it if necessary.
func GetSearchRegistry() *searchrun.Registry {
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

func (r *executionRouter) Reconfigure(_ *config.File) {
	r.searchReg.ReconfigureFromConfig()
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

// sshDialConn couples a dialed SSH channel (net.Conn) with the SSH client that
// owns it, so closing the conn also releases the client — no leaked SSH session.
type sshDialConn struct {
	net.Conn
	closer io.Closer
}

func (c *sshDialConn) Close() error {
	err := c.Conn.Close()
	if c.closer != nil {
		_ = c.closer.Close()
	}
	return err
}

func (e *sshFallbackExecutor) DialUpstream(_ context.Context, user string, r hosts.Record, address string) (net.Conn, error) {
	hc, err := e.Dial(user, r)
	if err != nil {
		return nil, fmt.Errorf("ssh dial for upstream: %w", err)
	}
	leafer, ok := hc.(interface{ LeafSSH() *ssh.Client })
	if !ok {
		_ = hc.Close()
		return nil, fmt.Errorf("ssh client has no leaf for upstream dial")
	}
	leaf := leafer.LeafSSH()
	if leaf == nil {
		_ = hc.Close()
		return nil, fmt.Errorf("ssh leaf client unavailable")
	}
	conn, err := leaf.Dial("tcp", address)
	if err != nil {
		_ = hc.Close()
		return nil, fmt.Errorf("ssh channel dial %s: %w", address, err)
	}
	return &sshDialConn{Conn: conn, closer: hc}, nil
}

// buildHostExecRegistry constructs the host execution registry.
func buildHostExecRegistry() hostexec.Registry {
	return &executionRouter{searchReg: GetSearchRegistry()}
}

// GetExecRegistry returns the host execution registry for SSH/Docker/TrueNAS dispatch.
func GetExecRegistry() hostexec.Registry {
	return buildHostExecRegistry()
}
