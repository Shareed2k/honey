package truenasprovider

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

var (
	errTrueNASTunnelOnly          = errors.New("truenasprovider: TrueNAS API-shell record supports port-forward (tunnel) only")
	errTrueNASTunnelNotConfigured = errors.New("truenasprovider: TrueNAS API tunnel not configured (import internal/ui)")
)

var tunnelMu sync.RWMutex

var truenasRunTunnel func(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error

// SetRunTunnel registers the API-shell port-forward runner (from ui.init).
func SetRunTunnel(fn func(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error) {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	truenasRunTunnel = fn
}

var truenasDialUpstream func(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error)

// SetDialUpstream registers the in-memory upstream dialer for proxy use (from ui.init).
func SetDialUpstream(fn func(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error)) {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	truenasDialUpstream = fn
}

// TruenasTunnelUsesAPIShell reports whether RunTunnel should use the TrueNAS API-shell
// TCP dial bridge (guests without SSH primary_ip). Appliance and rows with IP use SSH.
func TruenasTunnelUsesAPIShell(r hosts.Record) bool {
	return r.Provider == "truenas" &&
		hosts.IsTrueNASAPIShellRecord(r) &&
		hosts.PrimaryIPTrimmed(r) == ""
}

type truenasExecutor struct{}

func (truenasExecutor) Dial(_ string, _ hosts.Record) (hostexec.HostClient, error) {
	return nil, errTrueNASTunnelOnly
}

func (truenasExecutor) RunInteractive(_ string, _ hosts.Record) error {
	return errTrueNASTunnelOnly
}

func (truenasExecutor) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	tunnelMu.RLock()
	fn := truenasRunTunnel
	tunnelMu.RUnlock()
	if fn == nil {
		return errTrueNASTunnelNotConfigured
	}
	return fn(ctx, user, r, localFwd, out)
}

func (truenasExecutor) DialUpstream(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error) {
	tunnelMu.RLock()
	fn := truenasDialUpstream
	tunnelMu.RUnlock()
	if fn == nil {
		return nil, errTrueNASTunnelNotConfigured
	}
	return fn(ctx, user, r, address)
}

var apiShellExecutor truenasExecutor

// APIShellExecutor returns the TrueNAS API-shell executor.
func APIShellExecutor() hostexec.Executor {
	return apiShellExecutor
}
