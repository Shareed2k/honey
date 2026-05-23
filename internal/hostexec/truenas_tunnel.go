package hostexec

import (
	"context"
	"io"

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

var truenasAPIShellExecutor truenasExecutor
