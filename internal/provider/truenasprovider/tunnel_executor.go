package truenasprovider

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

var (
	errTrueNASTunnelOnly          = errors.New("truenasprovider: TrueNAS API-shell record supports port-forward (tunnel) only")
	errTrueNASTunnelNotConfigured = errors.New("truenasprovider: TrueNAS API tunnel not configured (import internal/ui)")
)

// TunnelRunner runs the TrueNAS API-shell port-forward. UpstreamDialer dials an
// in-memory upstream connection for the proxy path. Both are implemented in the ui
// package and injected via NewFactory / NewAPIShellExecutor to keep truenasprovider
// a leaf package (ui imports truenasprovider, not vice versa).
type TunnelRunner interface {
	RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error
}

// UpstreamDialer dials an in-memory upstream connection through the API shell.
type UpstreamDialer interface {
	DialUpstream(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error)
}

// TunnelRunnerFunc adapts a plain function to the TunnelRunner interface.
type TunnelRunnerFunc func(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error

// RunTunnel calls f.
func (f TunnelRunnerFunc) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	return f(ctx, user, r, localFwd, out)
}

// UpstreamDialerFunc adapts a plain function to the UpstreamDialer interface.
type UpstreamDialerFunc func(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error)

// DialUpstream calls f.
func (f UpstreamDialerFunc) DialUpstream(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error) {
	return f(ctx, user, r, address)
}

// TruenasTunnelUsesAPIShell reports whether RunTunnel should use the TrueNAS API-shell
// TCP dial bridge (guests without SSH primary_ip). Appliance and rows with IP use SSH.
func TruenasTunnelUsesAPIShell(r hosts.Record) bool {
	return r.Provider == "truenas" &&
		r.IsTrueNASAPIShell() &&
		r.PrimaryIPTrimmed() == ""
}

type truenasExecutor struct {
	tunnel TunnelRunner
	dialer UpstreamDialer
}

func (truenasExecutor) Dial(_ string, _ hosts.Record) (hostexec.HostClient, error) {
	return nil, errTrueNASTunnelOnly
}

func (truenasExecutor) RunInteractive(_ string, _ hosts.Record) error {
	return errTrueNASTunnelOnly
}

func (e truenasExecutor) RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error {
	if e.tunnel == nil {
		return errTrueNASTunnelNotConfigured
	}
	return e.tunnel.RunTunnel(ctx, user, r, localFwd, out)
}

func (e truenasExecutor) DialUpstream(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error) {
	if e.dialer == nil {
		return nil, errTrueNASTunnelNotConfigured
	}
	return e.dialer.DialUpstream(ctx, user, r, address)
}

// NewAPIShellExecutor returns the TrueNAS API-shell executor configured with the
// injected tunnel runner and upstream dialer (both implemented in the ui package).
func NewAPIShellExecutor(tunnel TunnelRunner, dialer UpstreamDialer) hostexec.Executor {
	return truenasExecutor{tunnel: tunnel, dialer: dialer}
}
