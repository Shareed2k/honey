package hostexec

import (
	"context"
	"io"
	"net"

	"github.com/shareed2k/honey/internal/hosts"
)

// TruenasTunnelUsesAPIShell reports whether RunTunnel should use the TrueNAS API-shell
// TCP dial bridge (guests without SSH primary_ip). Appliance and rows with IP use SSH.
func TruenasTunnelUsesAPIShell(r hosts.Record) bool {
	return r.Provider == "truenas" &&
		hosts.IsTrueNASAPIShellRecord(r) &&
		hosts.PrimaryIPTrimmed(r) == ""
}

var truenasRunTunnel func(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error

// SetTrueNASRunTunnel registers the API-shell port-forward runner (from ui.init).
func SetTrueNASRunTunnel(fn func(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error) {
	regMu.Lock()
	defer regMu.Unlock()
	truenasRunTunnel = fn
}

type truenasExecutor struct{}

func (truenasExecutor) Dial(_ string, _ hosts.Record) (HostClient, error) {
	return nil, errTrueNASTunnelOnly
}

func (truenasExecutor) RunInteractive(_ string, _ hosts.Record) error {
	return errTrueNASTunnelOnly
}

func (truenasExecutor) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	regMu.RLock()
	fn := truenasRunTunnel
	regMu.RUnlock()
	if fn == nil {
		return errTrueNASTunnelNotConfigured
	}
	return fn(ctx, user, r, localFwd, out)
}

var truenasDialUpstream func(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error)

// SetTrueNASDialUpstream registers the in-memory upstream dialer for proxy use.
func SetTrueNASDialUpstream(fn func(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error)) {
	regMu.Lock()
	defer regMu.Unlock()
	truenasDialUpstream = fn
}

func (truenasExecutor) DialUpstream(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error) {
	regMu.RLock()
	fn := truenasDialUpstream
	regMu.RUnlock()
	if fn == nil {
		return nil, errTrueNASTunnelNotConfigured
	}
	return fn(ctx, user, r, address)
}

var truenasAPIShellExecutor truenasExecutor
