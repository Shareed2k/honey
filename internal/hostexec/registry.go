package hostexec

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

// Registry handles resolving and dispatching host execution.
type Registry interface {
	ForRecord(r hosts.Record) Executor
	Reconfigure(cfg *config.File)
	RunSSHTunnel(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error
	BorrowSSH(user string, hop hosts.Record) (interface{}, bool)
}

// StandardRegistry implements Registry using injected behavior.
type StandardRegistry struct {
	Resolver       func(hosts.Record, Registry) Executor
	Reloader       func(cfg *config.File)
	Dialer         func(user, hostAlias string, overridePort int, identityFile string) (HostClient, error)
	RunInteractive func(user string, r hosts.Record, recorder any) error
	RunTunnel      func(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error
	SSHBorrower    func(user string, hop hosts.Record) (interface{}, bool)
}

// ForRecord returns the Executor for a search row.
// Provider-specific dispatch is handled by the Resolver.
// SSH is the fallback when no provider claims the record.
func (r *StandardRegistry) ForRecord(rec hosts.Record) Executor {
	if r.Resolver != nil {
		if ex := r.Resolver(rec, r); ex != nil {
			return ex
		}
	}
	return &sshExecutor{reg: r}
}

// BorrowSSH delegates to the configured SSHBorrower hook if provided.
func (r *StandardRegistry) BorrowSSH(user string, hop hosts.Record) (interface{}, bool) {
	if r.SSHBorrower != nil {
		return r.SSHBorrower(user, hop)
	}
	return nil, false
}

// Reconfigure propagates config to all registered provider factories.
func (r *StandardRegistry) Reconfigure(cfg *config.File) {
	if r.Reloader != nil {
		r.Reloader(cfg)
	}
}

// RunSSHTunnel runs the SSH local-forward tunnel.
func (r *StandardRegistry) RunSSHTunnel(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error {
	if r.RunTunnel == nil {
		return errTunnelNotConfigured
	}
	return r.RunTunnel(ctx, user, host, sshPort, localFwd, out)
}

type sshExecutor struct {
	reg *StandardRegistry
}

func (e *sshExecutor) Dial(user string, r hosts.Record) (HostClient, error) {
	if e.reg == nil || e.reg.Dialer == nil {
		return nil, errDialNotConfigured
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return nil, errNoHostIP
	}
	override := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		override = p
	}
	identity := ""
	if id, ok := hosts.MetaSSHIdentityFile(&r); ok {
		identity = id
	}
	return e.reg.Dialer(user, host, override, identity)
}

func (e *sshExecutor) RunInteractive(user string, r hosts.Record) error {
	if e.reg == nil || e.reg.RunInteractive == nil {
		return errInteractiveNotConfigured
	}
	return e.reg.RunInteractive(user, r, nil)
}

func (e *sshExecutor) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	if e.reg == nil || e.reg.RunTunnel == nil {
		return errTunnelNotConfigured
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return errNoHostIP
	}
	override := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		override = p
	}
	return e.reg.RunTunnel(ctx, user, host, override, localFwd, out)
}

func (sshExecutor) DialUpstream(_ context.Context, _ string, _ hosts.Record, _ string) (net.Conn, error) {
	return nil, fmt.Errorf("sshExecutor.DialUpstream not implemented (use sshclient directly)")
}
