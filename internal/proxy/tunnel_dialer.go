package proxy

import (
	"context"
	"net"
)

// TunnelDialer uses a custom dial function to natively dial
// upstream connections in memory without opening an OS-level listener port.
type TunnelDialer struct {
	dialFn func(ctx context.Context, network, address string) (net.Conn, error)
}

// NewTunnelDialer creates a new generic TunnelDialer.
func NewTunnelDialer(dialFn func(ctx context.Context, network, address string) (net.Conn, error)) *TunnelDialer {
	return &TunnelDialer{
		dialFn: dialFn,
	}
}

// DialContext connects directly to the background tunnel using the provided dial function.
func (d *TunnelDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.dialFn(ctx, network, address)
}
