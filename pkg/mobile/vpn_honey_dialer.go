package mobile

import (
	"context"
	"net"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// honeyExecDialer adapts a hostexec.Executor into the shapes StartVPN's SOCKS5
// forwarder (internal/sshclient.StartDynamicForwardMulti) needs: sshclient's
// SSHDialer (Dial) and its unexported contextDialer fast path (DialContext,
// preferred — carries the SOCKS5 server's real per-connection ctx, same as
// *sshclient.SSHPool already gets). Both forward to Executor.DialUpstream: the
// upstream honey server (already authenticated over mTLS/token/libp2p mesh)
// does its own SSH dial to rec with a server-side credential and proxies
// address over the resulting WebSocket. No SSH private key ever leaves, or
// exists on, the phone for this path.
type honeyExecDialer struct {
	ex   hostexec.Executor
	user string
	rec  hosts.Record
}

func (d *honeyExecDialer) Dial(network, addr string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, addr)
}

func (d *honeyExecDialer) DialContext(ctx context.Context, _ string, addr string) (net.Conn, error) {
	return d.ex.DialUpstream(ctx, d.user, d.rec, addr)
}
