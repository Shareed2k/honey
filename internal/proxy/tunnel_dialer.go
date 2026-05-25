package proxy

import (
	"context"
	"net"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/ui"
)

// TunnelDialer uses a Honey UI Executor (like k8s or TrueNAS) to natively dial
// upstream connections in memory without opening an OS-level listener port.
type TunnelDialer struct {
	user string
	rec  hosts.Record
}

// NewTunnelDialer creates an in-memory proxy dialer for any supported Executor.
func NewTunnelDialer(_ context.Context, user string, r hosts.Record, _ string) (*TunnelDialer, error) {
	return &TunnelDialer{
		user: user,
		rec:  r,
	}, nil
}

// DialContext connects directly to the background tunnel using the Executor interface.
func (d *TunnelDialer) DialContext(ctx context.Context, _ string, address string) (net.Conn, error) {
	executor := ui.GetExecutor(d.rec)
	return executor.DialUpstream(ctx, d.user, d.rec, address)
}
