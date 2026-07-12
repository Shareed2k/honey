// Package meshnet holds the process-wide libp2p Host used to reach (and be
// reached by) honey backends flagged mesh: true, even when they sit behind
// NAT/CGNAT with no port-forward. It wraps a single go-libp2p Host behind a
// small, testable API: a self-hosted, generic (non-honey) libp2p relay node
// provides Circuit Relay v2 forwarding, and go-libp2p's built-in DCUtR
// subsystem upgrades that relayed connection to a direct, hole-punched one
// (typically over QUIC, a default go-libp2p transport) whenever possible.
//
// Modeled on internal/devmtls: package-level state behind a mutex, exported
// lifecycle functions rather than a struct callers instantiate.
package meshnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"
)

// ProtocolID is the libp2p stream protocol this package's DialPeer/Listener
// use to carry honeyprovider's HTTP traffic over a libp2p Stream. Exported
// so a later task (internal/provider/honeyprovider) doesn't need to
// duplicate or guess this string.
const ProtocolID = "/honey/federation/1.0.0"

var (
	errNotStarted = errors.New("meshnet: not started")
	errDisabled   = errors.New("meshnet: disabled")
)

// Config configures this process's own libp2p mesh identity. It is meshnet's
// own plain struct — the caller (internal/provider/honeyprovider) is
// responsible for translating config.Get().Mesh (internal/config's
// MeshConfig) into one of these; this package does not import internal/config.
type Config struct {
	// Enabled turns the mesh subsystem on. When false, Start is a no-op.
	Enabled bool

	// PrivateKey is this Host's libp2p identity key, base64-encoded
	// protobuf-serialized (the "for config file" format produced by
	// go-libp2p's crypto.MarshalPrivateKey + crypto.ConfigEncodeKey, and
	// consumed here the same way ipfs config files do: ConfigDecodeKey then
	// crypto.UnmarshalPrivateKey — confirmed via `go doc` on
	// github.com/libp2p/go-libp2p/core/crypto).
	PrivateKey string

	// RelayAddrs are multiaddrs of self-hosted, generic libp2p relay
	// node(s), e.g. "/ip4/1.2.3.4/udp/4001/quic-v1/p2p/<relay-peer-id>".
	// Parsed with multiaddr.NewMultiaddr and used both as AutoRelay's
	// static relay candidates (so this Host can obtain a relay reservation
	// and be dialable behind NAT) and as the initial relay connections
	// established during Start.
	RelayAddrs []string

	// ListenMesh, when true, additionally runs the Circuit Relay v2 relay
	// service on this Host (EnableRelayService), so this instance also acts
	// as a relay for other peers. Only relevant if this instance is itself
	// publicly reachable. Off by default.
	ListenMesh bool
}

// MeshStatus is a small package-local diagnostics struct — deliberately not
// the raw go-libp2p host.Host, to keep this package's public surface
// independent of the underlying library's types. Named MeshStatus rather
// than Status (which the brief's sketch used for both the type and the
// accessor function below) because Go does not allow a function and a type
// to share a package-level name; the accessor keeps the name "Status" since
// that's the one referenced by call-site shape (Status().Connected)
// elsewhere in this package's design.
type MeshStatus struct {
	PeerID    string
	Connected bool     // is a relay connection (or better) actually up
	Relays    []string // connected relay multiaddrs, if any
}

// meshHost is the subset of go-libp2p's host.Host that this package depends
// on: identity (ID), establishing the initial relay connection (Connect),
// opening/accepting ProtocolID streams (NewStream/SetStreamHandler),
// best-effort relay-connectivity checks in Status (Network), and releasing
// all of the Host's background goroutines/listeners (Close). Exists purely
// as a seam for tests, not as a general-purpose abstraction — so it is kept
// to only the methods actually called below, rather than embedding
// host.Host wholesale.
type meshHost interface {
	ID() peer.ID
	Connect(ctx context.Context, pi peer.AddrInfo) error
	NewStream(ctx context.Context, p peer.ID, pids ...protocol.ID) (network.Stream, error)
	SetStreamHandler(pid protocol.ID, handler network.StreamHandler)
	Network() network.Network
	Close() error
}

// newHost constructs the real libp2p host. Package-level var, swappable in
// meshnet_test.go so tests never touch a real relay/network.
var newHost = func(opts ...libp2p.Option) (meshHost, error) {
	return libp2p.New(opts...)
}

// state holds everything about a successfully started mesh Host.
type state struct {
	host       meshHost
	relayInfos []peer.AddrInfo
	listener   *streamListener
}

var (
	mu        sync.Mutex
	attempted bool // Start has been called (successfully or not) since the last Stop
	startErr  error
	enabled   bool
	cur       *state
)

// Start is idempotent: the first call (across the process) does the real
// work under a lock; every later call — concurrent or sequential, regardless
// of the cfg passed — returns the exact result (error or nil) of that first
// attempt without re-initializing. Call Stop to genuinely reset the
// singleton.
//
// It is a no-op (nil error, no host constructed) when cfg.Enabled is false,
// or when cfg.Enabled is true but cfg.PrivateKey or cfg.RelayAddrs is empty:
// config-layer validation upstream already prevents that combination from
// reaching here in the real app, but Start itself must not panic or attempt
// a real libp2p join with insufficient config.
func Start(ctx context.Context, cfg Config) error {
	mu.Lock()
	defer mu.Unlock()

	if attempted {
		return startErr
	}
	attempted = true
	enabled = cfg.Enabled

	if !cfg.Enabled || cfg.PrivateKey == "" || len(cfg.RelayAddrs) == 0 {
		startErr = nil
		return nil
	}

	sk, err := decodePrivateKey(cfg.PrivateKey)
	if err != nil {
		startErr = fmt.Errorf("meshnet: decode private key: %w", err)
		return startErr
	}

	relayInfos := make([]peer.AddrInfo, 0, len(cfg.RelayAddrs))
	for _, raw := range cfg.RelayAddrs {
		addr, err := ma.NewMultiaddr(raw)
		if err != nil {
			startErr = fmt.Errorf("meshnet: parse relay addr %q: %w", raw, err)
			return startErr
		}
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			startErr = fmt.Errorf("meshnet: resolve relay addr %q: %w", raw, err)
			return startErr
		}
		relayInfos = append(relayInfos, *info)
	}

	opts := []libp2p.Option{
		libp2p.Identity(sk),
		libp2p.EnableHolePunching(),
		libp2p.EnableAutoRelayWithStaticRelays(relayInfos),
	}
	if cfg.ListenMesh {
		opts = append(opts, libp2p.EnableRelayService())
	}

	host, err := newHost(opts...)
	if err != nil {
		startErr = fmt.Errorf("meshnet: create host: %w", err)
		return startErr
	}

	for _, ri := range relayInfos {
		// This synchronous Connect is a best-effort warm-up, not a
		// correctness requirement: libp2p.EnableAutoRelayWithStaticRelays
		// above already configures go-libp2p's AutoRelay subsystem to
		// independently retry connecting to (and obtaining a reservation
		// with) these exact same static relays in the background, on its
		// own schedule, regardless of whether this call succeeds — see
		// go-libp2p's p2p/host/autorelay/relay_finder.go
		// (findNodes/tryNode and maybeConnectToRelay/connectToRelay both
		// call host.Connect on a recurring backoff loop once the relay
		// finder starts). Confirmed by reading the pinned
		// github.com/libp2p/go-libp2p@v0.48.0 source.
		//
		// So a relay being transiently unreachable at this exact instant
		// must not be fatal: Start's result is latched (see the
		// idempotency contract above) and would otherwise disable mesh
		// for the rest of the process's life even after the relay
		// recovers. Log and move on; AutoRelay converges on its own.
		if err := host.Connect(ctx, ri); err != nil {
			zap.L().Warn("meshnet: warm-up connect to relay failed; AutoRelay will keep retrying in the background",
				zap.Stringer("relay", ri.ID), zap.Error(err))
		}
	}

	l := newStreamListener(host)
	host.SetStreamHandler(protocol.ID(ProtocolID), l.handle)

	cur = &state{host: host, relayInfos: relayInfos, listener: l}
	startErr = nil
	return nil
}

// teardown closes the underlying libp2p host. Named (rather than calling
// h.Close() directly) so there is exactly one place that releases the host's
// background goroutines/listeners — currently only Stop, since Start no
// longer treats a relay Connect failure as fatal (see the loop above) and so
// has no partial-failure path of its own once the host is constructed.
func teardown(h meshHost) error {
	return h.Close()
}

// Stop shuts down the mesh Host (if one is running) and resets the
// singleton so a subsequent Start genuinely re-initializes (calls newHost
// again) rather than replaying a stale result.
//
// Stop does not wait for in-flight DialPeer/Listener callers to finish: a
// concurrent call may observe the host mid-close and fail with an error —
// the same contract as closing a net.Listener while Accept is blocked, or
// an http.Transport mid-request. That failure is expected shutdown
// behavior, not corruption, and callers that need a clean drain should
// quiesce traffic before calling Stop.
func Stop(_ context.Context) error {
	mu.Lock()
	defer mu.Unlock()

	wasEnabled := enabled
	var closeErr error
	if cur != nil {
		cur.listener.Close()
		closeErr = teardown(cur.host)
	}

	cur = nil
	attempted = false
	startErr = nil
	enabled = false

	if closeErr != nil {
		return closeErr
	}
	if !wasEnabled {
		return errDisabled
	}
	return nil
}

// Enabled reports whether Start was (first) called with cfg.Enabled true.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

// DialPeer resolves peerAddr (a multiaddr string, possibly including a relay
// circuit path like "/p2p/<relay-id>/p2p-circuit/p2p/<target-id>"), opens a
// stream to it using ProtocolID, and adapts the resulting network.Stream
// into a net.Conn.
func DialPeer(ctx context.Context, peerAddr string) (net.Conn, error) {
	mu.Lock()
	s := cur
	mu.Unlock()
	if s == nil {
		return nil, errNotStarted
	}

	addr, err := ma.NewMultiaddr(peerAddr)
	if err != nil {
		return nil, fmt.Errorf("meshnet: parse peer addr %q: %w", peerAddr, err)
	}
	info, err := peer.AddrInfoFromP2pAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("meshnet: resolve peer addr %q: %w", peerAddr, err)
	}

	if err := s.host.Connect(ctx, *info); err != nil {
		return nil, fmt.Errorf("meshnet: connect to peer %s: %w", info.ID, err)
	}

	stream, err := s.host.NewStream(ctx, info.ID, protocol.ID(ProtocolID))
	if err != nil {
		return nil, fmt.Errorf("meshnet: open stream to %s: %w", info.ID, err)
	}

	return newStreamConn(s.host, stream), nil
}

// Listener returns a net.Listener whose Accept blocks on this Host's
// registered stream handler for ProtocolID, wrapping each accepted
// network.Stream with the same adapter DialPeer uses.
func Listener() (net.Listener, error) {
	mu.Lock()
	s := cur
	mu.Unlock()
	if s == nil {
		return nil, errNotStarted
	}
	return s.listener, nil
}

// Status reports this Host's own peer ID and, best-effort, whether a relay
// connection is currently established and which relay(s).
func Status() (MeshStatus, error) {
	mu.Lock()
	s := cur
	mu.Unlock()
	if s == nil {
		return MeshStatus{}, errNotStarted
	}

	st := MeshStatus{PeerID: s.host.ID().String()}
	nw := s.host.Network()
	for _, ri := range s.relayInfos {
		if nw == nil || nw.Connectedness(ri.ID) != network.Connected {
			continue
		}
		st.Connected = true
		st.Relays = append(st.Relays, ri.String())
	}
	return st, nil
}

// decodePrivateKey decodes cfg.PrivateKey: base64 (go-libp2p's "for config
// file" encoding, crypto.ConfigDecodeKey/ConfigEncodeKey) wrapping a
// protobuf-serialized key (crypto.MarshalPrivateKey/UnmarshalPrivateKey).
func decodePrivateKey(s string) (crypto.PrivKey, error) {
	raw, err := crypto.ConfigDecodeKey(s)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	sk, err := crypto.UnmarshalPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return sk, nil
}
