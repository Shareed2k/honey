package meshnet

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

// --- test helpers to build real (but throwaway) libp2p identities/addrs ---
// meshnet never talks to a real network in these tests; only Config's string
// fields need to be shaped like the real thing so meshnet's own parsing code
// (multiaddr.NewMultiaddr, peer.AddrInfoFromP2pAddr, crypto.ConfigDecodeKey +
// crypto.UnmarshalPrivateKey) runs unmocked, against real inputs.

func newTestPeerID() peer.ID {
	_, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		panic(err)
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		panic(err)
	}
	return id
}

func newTestPrivateKeyString() string {
	sk, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		panic(err)
	}
	raw, err := crypto.MarshalPrivateKey(sk)
	if err != nil {
		panic(err)
	}
	return crypto.ConfigEncodeKey(raw)
}

func newTestRelayAddr() (string, peer.ID) {
	id := newTestPeerID()
	return "/ip4/127.0.0.1/udp/4001/quic-v1/p2p/" + id.String(), id
}

// --- fakeConn: minimal network.Conn, just enough for RemotePeer() ---

type fakeConn struct {
	remote peer.ID
}

func (c *fakeConn) Close() error                               { return nil }
func (c *fakeConn) LocalPeer() peer.ID                         { return "" }
func (c *fakeConn) RemotePeer() peer.ID                        { return c.remote }
func (c *fakeConn) RemotePublicKey() crypto.PubKey             { return nil }
func (c *fakeConn) ConnState() network.ConnectionState         { return network.ConnectionState{} }
func (c *fakeConn) LocalMultiaddr() ma.Multiaddr               { return nil }
func (c *fakeConn) RemoteMultiaddr() ma.Multiaddr              { return nil }
func (c *fakeConn) Stat() network.ConnStats                    { return network.ConnStats{} }
func (c *fakeConn) Scope() network.ConnScope                   { return nil }
func (c *fakeConn) CloseWithError(network.ConnErrorCode) error { return nil }
func (c *fakeConn) ID() string                                 { return "fake-conn" }
func (c *fakeConn) NewStream(context.Context) (network.Stream, error) {
	return nil, errors.New("fakeConn: not implemented")
}
func (c *fakeConn) GetStreams() []network.Stream { return nil }
func (c *fakeConn) IsClosed() bool               { return false }
func (c *fakeConn) As(any) bool                  { return false }

var _ network.Conn = (*fakeConn)(nil)

// --- fakeStream: minimal network.Stream backed by an in-memory buffer ---

type fakeStream struct {
	*bytes.Buffer
	conn        *fakeConn
	resetCalls  int
	closeCalls  int
	deadlineSet int
}

func newFakeStream(remote peer.ID, payload string) *fakeStream {
	return &fakeStream{Buffer: bytes.NewBufferString(payload), conn: &fakeConn{remote: remote}}
}

func (s *fakeStream) Close() error                                 { s.closeCalls++; return nil }
func (s *fakeStream) CloseWrite() error                            { return nil }
func (s *fakeStream) CloseRead() error                             { return nil }
func (s *fakeStream) Reset() error                                 { s.resetCalls++; return nil }
func (s *fakeStream) ResetWithError(network.StreamErrorCode) error { s.resetCalls++; return nil }
func (s *fakeStream) SetDeadline(time.Time) error                  { s.deadlineSet++; return nil }
func (s *fakeStream) SetReadDeadline(time.Time) error              { s.deadlineSet++; return nil }
func (s *fakeStream) SetWriteDeadline(time.Time) error             { s.deadlineSet++; return nil }
func (s *fakeStream) ID() string                                   { return "fake-stream" }
func (s *fakeStream) Protocol() protocol.ID                        { return protocol.ID(ProtocolID) }
func (s *fakeStream) SetProtocol(protocol.ID) error                { return nil }
func (s *fakeStream) Stat() network.Stats                          { return network.Stats{} }
func (s *fakeStream) Conn() network.Conn                           { return s.conn }
func (s *fakeStream) Scope() network.StreamScope                   { return nil }

var _ network.Stream = (*fakeStream)(nil)

// --- fakeNetwork: minimal network.Network, just enough for Connectedness ---

type fakeNetwork struct {
	mu            sync.Mutex
	connectedness map[peer.ID]network.Connectedness
}

func newFakeNetwork() *fakeNetwork {
	return &fakeNetwork{connectedness: map[peer.ID]network.Connectedness{}}
}

func (n *fakeNetwork) setConnectedness(p peer.ID, c network.Connectedness) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.connectedness[p] = c
}

func (n *fakeNetwork) Peerstore() peerstore.Peerstore { return nil }
func (n *fakeNetwork) LocalPeer() peer.ID             { return "" }
func (n *fakeNetwork) DialPeer(context.Context, peer.ID) (network.Conn, error) {
	return nil, errors.New("fakeNetwork: not implemented")
}
func (n *fakeNetwork) ClosePeer(peer.ID) error { return nil }
func (n *fakeNetwork) Connectedness(p peer.ID) network.Connectedness {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.connectedness[p]
}
func (n *fakeNetwork) Peers() []peer.ID                       { return nil }
func (n *fakeNetwork) Conns() []network.Conn                  { return nil }
func (n *fakeNetwork) ConnsToPeer(peer.ID) []network.Conn     { return nil }
func (n *fakeNetwork) Notify(network.Notifiee)                {}
func (n *fakeNetwork) StopNotify(network.Notifiee)            {}
func (n *fakeNetwork) CanDial(peer.ID, ma.Multiaddr) bool     { return true }
func (n *fakeNetwork) Close() error                           { return nil }
func (n *fakeNetwork) SetStreamHandler(network.StreamHandler) {}
func (n *fakeNetwork) NewStream(context.Context, peer.ID) (network.Stream, error) {
	return nil, errors.New("fakeNetwork: not implemented")
}
func (n *fakeNetwork) Listen(...ma.Multiaddr) error                      { return nil }
func (n *fakeNetwork) ListenAddresses() []ma.Multiaddr                   { return nil }
func (n *fakeNetwork) InterfaceListenAddresses() ([]ma.Multiaddr, error) { return nil, nil }
func (n *fakeNetwork) ResourceManager() network.ResourceManager          { return nil }

var _ network.Network = (*fakeNetwork)(nil)

// --- fakeHost: the meshHost seam's fake, with call counters/injectable errors ---

type fakeHost struct {
	id peer.ID

	mu             sync.Mutex
	network        *fakeNetwork
	connectErr     error
	connectDelay   time.Duration // if >0, Connect blocks this long or until ctx is done, whichever first
	connectCalls   int
	newStreamErr   error
	newStreamRet   network.Stream
	newStreamCalls int
	closeErr       error
	closeCalls     int
	handler        network.StreamHandler
}

func newFakeHost() *fakeHost {
	return &fakeHost{id: newTestPeerID(), network: newFakeNetwork()}
}

func (f *fakeHost) ID() peer.ID { return f.id }

// Connect mimics real go-libp2p Host.Connect's ctx-respecting behavior: when
// connectDelay is set (e.g. to simulate an unreachable relay that would hang
// far longer than any caller-imposed timeout), it blocks until either that
// delay elapses or ctx is canceled/times out first, returning ctx.Err() in
// the latter case -- exactly the shape meshnet.Start's per-relay
// context.WithTimeout wrapper is meant to bound.
func (f *fakeHost) Connect(ctx context.Context, _ peer.AddrInfo) error {
	f.mu.Lock()
	f.connectCalls++
	err := f.connectErr
	delay := f.connectDelay
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *fakeHost) NewStream(_ context.Context, _ peer.ID, _ ...protocol.ID) (network.Stream, error) {
	f.mu.Lock()
	f.newStreamCalls++
	err, ret := f.newStreamErr, f.newStreamRet
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (f *fakeHost) SetStreamHandler(_ protocol.ID, handler network.StreamHandler) {
	f.mu.Lock()
	f.handler = handler
	f.mu.Unlock()
}

func (f *fakeHost) Network() network.Network {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.network == nil {
		return nil
	}
	return f.network
}

func (f *fakeHost) Close() error {
	f.mu.Lock()
	f.closeCalls++
	err := f.closeErr
	f.mu.Unlock()
	return err
}

func (f *fakeHost) callHandler(s network.Stream) {
	f.mu.Lock()
	h := f.handler
	f.mu.Unlock()
	h(s)
}

var _ meshHost = (*fakeHost)(nil)
