package meshnet

import (
	"net"
	"sync"

	"github.com/libp2p/go-libp2p/core/network"
)

// meshAddr is a minimal net.Addr backed by a libp2p peer ID string, since
// libp2p addressing isn't IP-based the way net.Conn's addr types assume.
type meshAddr struct{ id string }

func (a meshAddr) Network() string { return "libp2p" }
func (a meshAddr) String() string  { return a.id }

// streamConn adapts a network.Stream into a net.Conn. network.Stream (via
// its embedded MuxedStream) already implements Read/Write/Close and
// SetDeadline/SetReadDeadline/SetWriteDeadline — confirmed via
// `go doc github.com/libp2p/go-libp2p/core/network Stream` and
// `... MuxedStream` — so embedding it here only leaves LocalAddr/RemoteAddr
// to add, derived from the local Host's and remote peer's IDs.
type streamConn struct {
	network.Stream
	local  net.Addr
	remote net.Addr
}

func newStreamConn(h meshHost, s network.Stream) net.Conn {
	return &streamConn{
		Stream: s,
		local:  meshAddr{id: h.ID().String()},
		remote: meshAddr{id: s.Conn().RemotePeer().String()},
	}
}

func (c *streamConn) LocalAddr() net.Addr  { return c.local }
func (c *streamConn) RemoteAddr() net.Addr { return c.remote }

// streamListener bridges go-libp2p's callback-style stream handler
// (network.StreamHandler, registered via host.SetStreamHandler — confirmed
// via `go doc github.com/libp2p/go-libp2p/core/host Host`) into a
// synchronous net.Listener: the handler pushes each accepted stream onto
// acceptCh, and Accept reads from it. Closing doneCh (once, via Close)
// unblocks any pending Accept and causes future handler callbacks to reset
// the stream instead of blocking forever.
type streamListener struct {
	host      meshHost
	acceptCh  chan network.Stream
	doneCh    chan struct{}
	closeOnce sync.Once
}

func newStreamListener(h meshHost) *streamListener {
	return &streamListener{
		host:     h,
		acceptCh: make(chan network.Stream),
		doneCh:   make(chan struct{}),
	}
}

// handle is the network.StreamHandler registered for ProtocolID.
func (l *streamListener) handle(s network.Stream) {
	select {
	case l.acceptCh <- s:
	case <-l.doneCh:
		_ = s.Reset()
	}
}

func (l *streamListener) Accept() (net.Conn, error) {
	select {
	case s := <-l.acceptCh:
		return newStreamConn(l.host, s), nil
	case <-l.doneCh:
		return nil, net.ErrClosed
	}
}

func (l *streamListener) Close() error {
	l.closeOnce.Do(func() { close(l.doneCh) })
	return nil
}

func (l *streamListener) Addr() net.Addr {
	return meshAddr{id: l.host.ID().String()}
}
