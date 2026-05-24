package proxy

import (
	"context"
	"net"

	"github.com/shareed2k/honey/internal/sshclient"
)

// Dialer abstracts how we dial to an upstream service.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// DirectDialer connects directly over the local network.
type DirectDialer struct{}

// DialContext implements Dialer.
func (d DirectDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var nd net.Dialer
	return nd.DialContext(ctx, network, address)
}

// SSHDialer connects to the upstream by tunneling through an SSH connection.
type SSHDialer struct {
	Client *sshclient.HoneyClient
}

// DialContext implements Dialer.
func (d SSHDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}

	ch := make(chan result, 1)

	go func() {
		conn, err := d.Client.Dial(network, address)
		ch <- result{conn: conn, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.conn, r.err
	}
}
