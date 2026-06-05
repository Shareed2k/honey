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
	BorrowSSH(user string, hop hosts.Record) (any, bool)
}

// The interfaces below are the dependencies StandardRegistry needs from
// higher-level packages. They are declared here — on the consumer side — so
// hostexec stays a leaf package: sshclient, searchrun, ui and provider/* all
// import hostexec, never the reverse. The composition root (internal/cli)
// supplies concrete implementations, wrapping free functions in the *Func
// adapters below (the http.HandlerFunc idiom).

// ExecutorResolver resolves a provider-specific Executor for a record,
// returning nil to fall back to SSH.
type ExecutorResolver interface {
	ResolveExecutor(rec hosts.Record, reg Registry) Executor
}

// Reconfigurer applies updated configuration to provider factories.
type Reconfigurer interface {
	ReconfigureFromConfig(cfg *config.File)
}

// Dialer establishes a HostClient for an SSH target.
type Dialer interface {
	DialHost(user, hostAlias string, overridePort int, identityFile string) (HostClient, error)
}

// InteractiveRunner runs an interactive SSH session on a record.
type InteractiveRunner interface {
	RunInteractive(user string, r hosts.Record, recorder any) error
}

// TunnelRunner runs an SSH local-forward tunnel until ctx is cancelled.
type TunnelRunner interface {
	RunTunnel(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error
}

// SSHBorrower hands out a cached SSH client for a hop, if one is available.
type SSHBorrower interface {
	BorrowSSH(user string, hop hosts.Record) (any, bool)
}

// ExecutorResolverFunc adapts a plain function to the ExecutorResolver interface.
type ExecutorResolverFunc func(rec hosts.Record, reg Registry) Executor

// ResolveExecutor calls f.
func (f ExecutorResolverFunc) ResolveExecutor(rec hosts.Record, reg Registry) Executor {
	return f(rec, reg)
}

// ReconfigurerFunc adapts a plain function to the Reconfigurer interface.
type ReconfigurerFunc func(cfg *config.File)

// ReconfigureFromConfig calls f.
func (f ReconfigurerFunc) ReconfigureFromConfig(cfg *config.File) {
	f(cfg)
}

// DialerFunc adapts a plain function to the Dialer interface.
type DialerFunc func(user, hostAlias string, overridePort int, identityFile string) (HostClient, error)

// DialHost calls f.
func (f DialerFunc) DialHost(user, hostAlias string, overridePort int, identityFile string) (HostClient, error) {
	return f(user, hostAlias, overridePort, identityFile)
}

// InteractiveRunnerFunc adapts a plain function to the InteractiveRunner interface.
type InteractiveRunnerFunc func(user string, r hosts.Record, recorder any) error

// RunInteractive calls f.
func (f InteractiveRunnerFunc) RunInteractive(user string, r hosts.Record, recorder any) error {
	return f(user, r, recorder)
}

// TunnelRunnerFunc adapts a plain function to the TunnelRunner interface.
type TunnelRunnerFunc func(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error

// RunTunnel calls f.
func (f TunnelRunnerFunc) RunTunnel(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error {
	return f(ctx, user, host, sshPort, localFwd, out)
}

// SSHBorrowerFunc adapts a plain function to the SSHBorrower interface.
type SSHBorrowerFunc func(user string, hop hosts.Record) (any, bool)

// BorrowSSH calls f.
func (f SSHBorrowerFunc) BorrowSSH(user string, hop hosts.Record) (any, bool) {
	return f(user, hop)
}

// StandardRegistry implements Registry by delegating to injected collaborators.
// A nil collaborator disables the corresponding capability.
type StandardRegistry struct {
	Resolver     ExecutorResolver
	Reconfigurer Reconfigurer
	Dialer       Dialer
	Interactive  InteractiveRunner
	Tunnel       TunnelRunner
	SSHBorrower  SSHBorrower
}

// ForRecord returns the Executor for a search row.
// Provider-specific dispatch is handled by the Resolver.
// SSH is the fallback when no provider claims the record.
func (r *StandardRegistry) ForRecord(rec hosts.Record) Executor {
	if r.Resolver != nil {
		if ex := r.Resolver.ResolveExecutor(rec, r); ex != nil {
			return ex
		}
	}
	return &sshExecutor{reg: r}
}

// BorrowSSH delegates to the configured SSHBorrower if provided.
func (r *StandardRegistry) BorrowSSH(user string, hop hosts.Record) (any, bool) {
	if r.SSHBorrower != nil {
		return r.SSHBorrower.BorrowSSH(user, hop)
	}
	return nil, false
}

// Reconfigure propagates config to all registered provider factories.
func (r *StandardRegistry) Reconfigure(cfg *config.File) {
	if r.Reconfigurer != nil {
		r.Reconfigurer.ReconfigureFromConfig(cfg)
	}
}

// RunSSHTunnel runs the SSH local-forward tunnel.
func (r *StandardRegistry) RunSSHTunnel(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error {
	if r.Tunnel == nil {
		return errTunnelNotConfigured
	}
	return r.Tunnel.RunTunnel(ctx, user, host, sshPort, localFwd, out)
}

type sshExecutor struct {
	reg *StandardRegistry
}

func (e *sshExecutor) Dial(user string, r hosts.Record) (HostClient, error) {
	if e.reg == nil || e.reg.Dialer == nil {
		return nil, errDialNotConfigured
	}
	user = strings.TrimSpace(user)
	if user == "" {
		if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
			user = u
		}
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
	return e.reg.Dialer.DialHost(user, host, override, identity)
}

func (e *sshExecutor) RunInteractive(user string, r hosts.Record) error {
	if e.reg == nil || e.reg.Interactive == nil {
		return errInteractiveNotConfigured
	}
	user = strings.TrimSpace(user)
	if user == "" {
		if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
			user = u
		}
	}
	return e.reg.Interactive.RunInteractive(user, r, nil)
}

func (e *sshExecutor) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	if e.reg == nil || e.reg.Tunnel == nil {
		return errTunnelNotConfigured
	}
	user = strings.TrimSpace(user)
	if user == "" {
		if u := strings.TrimSpace(r.Meta["ssh_user"]); u != "" {
			user = u
		}
	}
	host := strings.TrimSpace(r.PrimaryIP)
	if host == "" {
		return errNoHostIP
	}
	override := 0
	if p, ok := hosts.MetaSSHPort(&r); ok {
		override = p
	}
	return e.reg.Tunnel.RunTunnel(ctx, user, host, override, localFwd, out)
}

func (sshExecutor) DialUpstream(_ context.Context, _ string, _ hosts.Record, _ string) (net.Conn, error) {
	return nil, fmt.Errorf("sshExecutor.DialUpstream not implemented (use sshclient directly)")
}
