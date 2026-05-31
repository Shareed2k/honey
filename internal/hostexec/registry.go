package hostexec

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

var (
	regMu sync.RWMutex

	// executorResolver is wired once by searchrun.init() to dispatch records to provider executors.
	executorResolver func(hosts.Record) Executor

	// configReloader is wired once by searchrun.init() to dispatch ReconfigureFromHoneyConfig to all provider factories.
	configReloader func(cfg *config.File)

	// dialHoneyHost connects to PrimaryIP (or alias) via SSH; wired by internal/sshclient init.
	dialHoneyHost func(user, hostAlias string, overridePort int, identityFile string) (HostClient, error)

	// sshRunInteractive opens a local TTY session; wired by internal/ui init.
	sshRunInteractive func(user string, r hosts.Record, recorder any) error

	// sshRunTunnel runs SSH -L style forwarding; wired by internal/sshclient init.
	sshRunTunnel func(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error
)

// SetConfigReloader wires a provider config reloader (called from searchrun.init).
func SetConfigReloader(fn func(cfg *config.File)) {
	regMu.Lock()
	defer regMu.Unlock()
	configReloader = fn
}

// SetExecutorResolver wires the provider registry's executor dispatch (called from searchrun.init).
func SetExecutorResolver(fn func(hosts.Record) Executor) {
	regMu.Lock()
	defer regMu.Unlock()
	executorResolver = fn
}

// SetDialHoney registers the SSH HostClient dialer (from sshclient.init).
func SetDialHoney(fn func(user, hostAlias string, overridePort int, identityFile string) (HostClient, error)) {
	regMu.Lock()
	defer regMu.Unlock()
	dialHoneyHost = fn
}

// SetSSHRunInteractive registers the TTY interactive runner (from ui.init).
func SetSSHRunInteractive(fn func(user string, r hosts.Record, recorder any) error) {
	regMu.Lock()
	defer regMu.Unlock()
	sshRunInteractive = fn
}

// SetSSHRunTunnel registers the SSH local-forward tunnel runner (from sshclient.init).
func SetSSHRunTunnel(fn func(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error) {
	regMu.Lock()
	defer regMu.Unlock()
	sshRunTunnel = fn
}

// ReconfigureFromHoneyConfig propagates config to all registered provider factories.
// Safe to call from CLI after loading config and from the web server on startup.
func ReconfigureFromHoneyConfig(cfg *config.File) {
	regMu.RLock()
	fn := configReloader
	regMu.RUnlock()
	if fn != nil {
		fn(cfg)
	}
}

// RunSSHTunnel runs the SSH local-forward tunnel registered by sshclient.
func RunSSHTunnel(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error {
	regMu.RLock()
	fn := sshRunTunnel
	regMu.RUnlock()
	if fn == nil {
		return errTunnelNotConfigured
	}
	return fn(ctx, user, host, sshPort, localFwd, out)
}

type sshExecutor struct{}

func (sshExecutor) Dial(user string, r hosts.Record) (HostClient, error) {
	if dialHoneyHost == nil {
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
	return dialHoneyHost(user, host, override, identity)
}

func (sshExecutor) RunInteractive(user string, r hosts.Record) error {
	if sshRunInteractive == nil {
		return errInteractiveNotConfigured
	}
	return sshRunInteractive(user, r, nil)
}

func (sshExecutor) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	if sshRunTunnel == nil {
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
	return sshRunTunnel(ctx, user, host, override, localFwd, out)
}

var defaultSSHExecutor = sshExecutor{}

// ForRecord returns the Executor for a search row.
// Provider-specific dispatch is handled by the resolver registered via SetExecutorResolver (searchrun).
// SSH is the fallback when no provider claims the record.
func ForRecord(r hosts.Record) Executor {
	regMu.RLock()
	fn := executorResolver
	regMu.RUnlock()
	if fn != nil {
		if ex := fn(r); ex != nil {
			return ex
		}
	}
	return defaultSSHExecutor
}

func (sshExecutor) DialUpstream(_ context.Context, _ string, _ hosts.Record, _ string) (net.Conn, error) {
	return nil, fmt.Errorf("sshExecutor.DialUpstream not implemented (use sshclient directly)")
}
