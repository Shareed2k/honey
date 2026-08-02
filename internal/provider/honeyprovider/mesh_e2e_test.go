package honeyprovider

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/meshnet"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	circuitclient "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/require"
)

// TestHoney_MeshE2E_TwoDistinctPeers proves the real, two-*distinct*-peer
// mesh round trip that tests/integration/honey_provider_mesh_e2e_test.go's
// TestHoneyProviderMeshE2E_SelfDialIsRejectedByLibp2p cannot: a real HTTP
// request, issued by *this* package's own dial-and-parse code (Honey.Search
// -> buildTransport -> meshDialContext -> meshDial), flowing over a real
// libp2p stream, through a real, independent Circuit Relay v2 relay, to a
// genuinely different peer identity -- and a real HTTP response flowing
// back.
//
// Why three Hosts, and why this sidesteps task 7's finding entirely:
// task 7 (see task-7-report.md) proved that go-libp2p's swarm unconditionally
// rejects any dial where the target peer ID equals the dialing Host's own
// (p2p/net/swarm/swarm_dial.go's ErrDialToSelf, "dial to self attempted") --
// a check that fires before any transport, including a relay circuit, is
// even attempted. tests/integration can only reach the single
// internal/meshnet process-wide singleton, so it can only ever prove (or, as
// it turns out, disprove) a *self*-dial. This package, uniquely, owns a
// white-box seam that singleton doesn't offer: honeyprovider's package-level
// `var meshDial = meshnet.DialPeer` (transport.go) is reassignable from this
// package's own `_test.go` files (already exploited by transport_test.go's
// TestMeshDialContext/TestBuildTransport/TestBuildWSDialer and
// transport_test.go's TestBackendRows_MeshRouting, though those stub meshDial
// out entirely rather than routing it through a real libp2p Host). Swapping
// meshDial to a closure backed by a genuinely separate, real libp2p Host --
// a THIRD identity, distinct from both the relay and the "server" peer this
// test also constructs -- means there is no self-dial anywhere in this
// topology, so ErrDialToSelf simply never applies here.
//
// Topology:
//  1. relayHost: the exact same construction task 7 verified and documented
//     (libp2p.New + EnableRelayService + ForceReachabilityPublic -- see
//     task-7-report.md for why ForceReachabilityPublic is required for the
//     relay service to actually activate on a loopback-only test host).
//  2. serverHost: a second, independent libp2p.New host. It connects to the
//     relay and reserves a Circuit Relay v2 slot via
//     circuitclient.Reserve(ctx, serverHost, relayInfo) (go doc
//     github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client Reserve:
//     "Clients must reserve slots in order for the relay to relay
//     connections to them.") -- this is the piece task 7's self-dial test
//     never needed (its one singleton Host was both server and would-be
//     client, so nothing ever needed to dial *in* to it from a separate
//     peer). Without this reservation, the relay would reject any inbound
//     HOP CONNECT to serverHost with a "no reservation" status regardless of
//     whether the dialer is a distinct peer. serverHost registers a stream
//     handler on meshnet.ProtocolID (the same exported protocol constant
//     internal/meshnet uses, reused verbatim rather than re-declared) that
//     reads an HTTP/1.1 request off the raw stream and writes back a
//     hand-rolled HTTP/1.1 response whose JSON body matches this package's
//     own searchResponse{Records []hosts.Record} shape (honey.go) --
//     deliberately not a real internal/webserver instance: this test's job
//     is proving honeyprovider's own dial-and-parse code works over a real
//     stream, not re-proving internal/webserver's routing (already covered
//     elsewhere).
//  3. clientHost: a third, independent libp2p.New host, captured by a
//     closure that replaces this package's meshDial var for the duration of
//     the test (restored via t.Cleanup, matching transport_test.go's own
//     convention for swapping this same shared, mutable package state).
//     The closure mirrors meshnet.DialPeer's own real logic exactly
//     (ma.NewMultiaddr -> peer.AddrInfoFromP2pAddr -> host.Connect ->
//     host.NewStream(meshnet.ProtocolID)) but through clientHost instead of
//     the meshnet singleton, then adapts the resulting network.Stream into a
//     net.Conn (network.Stream already satisfies net.Conn's Read/Write/
//     Close/deadline methods via its embedded MuxedStream -- confirmed via
//     `go doc github.com/libp2p/go-libp2p/core/network Stream`/
//     `... MuxedStream` -- so only LocalAddr/RemoteAddr need adding, exactly
//     the gap internal/meshnet's own unexported streamConn fills for
//     meshnet.DialPeer's callers).
//  4. A real *Honey{Mesh: true, MeshAddr: <circuit addr to serverHost>} is
//     driven through .Search(...) exactly like the package's other tests
//     drive *Honey/*Executor/*Client, and the real returned record is
//     asserted against what the hand-rolled server wrote back.
func TestHoney_MeshE2E_TwoDistinctPeers(t *testing.T) {
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer setupCancel()

	// 1. Relay host -- see task-7-report.md; reused verbatim.
	relayHost, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
		libp2p.EnableRelayService(),
		libp2p.ForceReachabilityPublic(),
	)
	require.NoError(t, err, "construct relay host")
	t.Cleanup(func() { _ = relayHost.Close() })

	relayAddrs := relayHost.Addrs()
	require.NotEmpty(t, relayAddrs, "relay host has no listen addresses")
	relayMultiaddr := relayAddrs[0].String() + "/p2p/" + relayHost.ID().String()
	relayInfo := peer.AddrInfo{ID: relayHost.ID(), Addrs: relayAddrs}

	// 2. Server host: a genuinely distinct peer identity from the relay.
	serverHost, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err, "construct server host")
	t.Cleanup(func() { _ = serverHost.Close() })

	require.NoError(t, serverHost.Connect(setupCtx, relayInfo), "server connect to relay")
	_, err = circuitclient.Reserve(setupCtx, serverHost, relayInfo)
	require.NoError(t, err, "server reserve relay slot")

	const wantRecordJSON = `{"records":[{"name":"remote-host","primary_ip":"10.0.0.1"}]}`
	serverHost.SetStreamHandler(protocol.ID(meshnet.ProtocolID), func(s network.Stream) {
		defer s.Close()

		req, err := http.ReadRequest(bufio.NewReader(s))
		if err != nil {
			return
		}
		_ = req.Body.Close()

		resp := &http.Response{
			StatusCode:    http.StatusOK,
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        http.Header{"Content-Type": {"application/json"}},
			Body:          io.NopCloser(strings.NewReader(wantRecordJSON)),
			ContentLength: int64(len(wantRecordJSON)),
		}
		_ = resp.Write(s)
	})

	// 3. Client host: the THIRD distinct peer identity -- the one that
	// actually dials, standing in for what internal/meshnet's singleton
	// Host would do in production (where it is never also the target, since
	// a real deployment always has two distinct honey instances).
	clientHost, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err, "construct client host")
	t.Cleanup(func() { _ = clientHost.Close() })

	origMeshDial := meshDial
	meshDial = func(ctx context.Context, peerAddr string) (net.Conn, error) {
		addr, err := ma.NewMultiaddr(peerAddr)
		if err != nil {
			return nil, fmt.Errorf("parse mesh addr %q: %w", peerAddr, err)
		}
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			return nil, fmt.Errorf("resolve mesh addr %q: %w", peerAddr, err)
		}
		if err := clientHost.Connect(ctx, *info); err != nil {
			return nil, fmt.Errorf("connect to %s: %w", info.ID, err)
		}
		// The connection to info.ID goes through the relay circuit, so
		// go-libp2p's swarm considers it "limited" -- by default,
		// Swarm.NewStream refuses to open a stream over a limited
		// connection and instead blocks waiting for it to upgrade to a
		// direct connection via hole punching/connection reversal (see
		// `go doc` on p2p/net/swarm/swarm.go's own NewStream comment:
		// "Use network.WithAllowLimitedConn to open a stream over a limited
		// (relayed) connection."). Neither serverHost nor clientHost enables
		// hole punching in this test (this test's purpose is proving the
		// relay path itself, not DCUtR upgrade), so without this the wait
		// would block until network.GetDialPeerTimeout's default (10s) and
		// then fail with "context deadline exceeded" -- confirmed by
		// hitting exactly that failure while developing this test, then
		// finding p2p/net/swarm/swarm.go's NewStream/waitForDirectConn via
		// `go doc`/source reading. WithAllowLimitedConn explicitly opts in
		// to using the relayed connection as-is, which is the real,
		// intended behavior for a mesh peer reached only via relay (a
		// production peer that's never directly reachable should still be
		// dialable -- the whole point of the relay).
		ctx = network.WithAllowLimitedConn(ctx, "honeyprovider mesh e2e test: dial via circuit relay")
		stream, err := clientHost.NewStream(ctx, info.ID, protocol.ID(meshnet.ProtocolID))
		if err != nil {
			return nil, fmt.Errorf("open stream to %s: %w", info.ID, err)
		}
		return &testMeshStreamConn{
			Stream: stream,
			local:  testMeshAddr(clientHost.ID().String()),
			remote: testMeshAddr(info.ID.String()),
		}, nil
	}
	t.Cleanup(func() { meshDial = origMeshDial })

	// 4. Circuit-relay multiaddr to the (distinct) server peer, and a real
	// Honey backend routed through it.
	serverMeshAddr := relayMultiaddr + "/p2p-circuit/p2p/" + serverHost.ID().String()

	h := &Honey{Name: "mesh-peer", URL: "http://mesh-peer/", Mesh: true, MeshAddr: serverMeshAddr}

	searchCtx, searchCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer searchCancel()
	recs, err := h.Search(searchCtx, hosts.Query{NameSubstring: "remote"})
	require.NoError(t, err, "mesh search")
	require.Len(t, recs, 1)
	require.Equal(t, "remote-host", recs[0].Name)
	require.Equal(t, "10.0.0.1", recs[0].PrimaryIP)
}

// testMeshAddr is a minimal net.Addr backed by a libp2p peer ID string,
// mirroring internal/meshnet's own unexported meshAddr (stream_conn.go) --
// duplicated here rather than imported since that type is unexported and
// this test deliberately does not depend on internal/meshnet's Host/Start
// machinery at all, only its exported ProtocolID/DialPeer-shaped contract.
type testMeshAddr string

func (a testMeshAddr) Network() string { return "libp2p" }
func (a testMeshAddr) String() string  { return string(a) }

// testMeshStreamConn adapts a network.Stream into a net.Conn, the same gap
// internal/meshnet's own unexported streamConn fills: network.Stream (via
// its embedded MuxedStream) already implements Read/Write/Close/
// SetDeadline/SetReadDeadline/SetWriteDeadline (confirmed via
// `go doc github.com/libp2p/go-libp2p/core/network Stream`/
// `... MuxedStream`), leaving only LocalAddr/RemoteAddr to add.
type testMeshStreamConn struct {
	network.Stream
	local  net.Addr
	remote net.Addr
}

func (c *testMeshStreamConn) LocalAddr() net.Addr  { return c.local }
func (c *testMeshStreamConn) RemoteAddr() net.Addr { return c.remote }
