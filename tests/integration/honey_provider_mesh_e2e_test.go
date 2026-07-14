//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/meshnet"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
	"github.com/shareed2k/honey/internal/provider/localprovider"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/webserver"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
)

// TestHoneyProviderMeshE2E_SelfDialIsRejectedByLibp2p exercises the full mesh
// chain -- config (Mesh/MeshAddr) -> honeyprovider.buildTransport ->
// meshDialContext -> meshDial (meshnet.DialPeer) -> a real libp2p stream,
// forced through a genuinely separate, real Circuit Relay v2 relay process
// (not a shortcut/loopback bypass) -> meshnet.Listener() -> the webserver's
// real HTTP handler -- and asserts the one outcome that topology can
// actually produce: a deterministic go-libp2p self-dial rejection.
//
// Why this is a self-dial, and why that's correct (not a shortcut):
// internal/meshnet is a package-level singleton (one process-wide libp2p
// Host, guarded by a package mutex -- see internal/meshnet/meshnet.go's own
// doc comment, deliberately modeled on internal/devmtls's same pattern). A
// single Go test binary can only have one active meshnet.Start'd identity at
// a time, so this test cannot simulate "two independent honey instances,
// each with their own mesh identity" the way TestHoneyProviderMTLS_E2E
// simulates two independent HTTP-level actors. There is only one meshnet
// singleton available to this test binary, and it plays both roles at once:
//   - it's the Host backing this webserver's EnableMesh listener (the
//     "server" side), and
//   - it's the Host meshnet.DialPeer/honeyprovider dial through (the
//     "client" side).
//
// So the only topology available here is to dial that one Host's own peer
// ID, through a real, independent, external relay (constructed directly via
// libp2p.New with its own peer identity -- NOT internal/meshnet -- playing
// the role of "a generic, independent, non-honey libp2p relay node", exactly
// the external infrastructure the real feature depends on).
//
// EMPIRICALLY CONFIRMED FINDING (see task-7-report.md for the full writeup,
// reused unchanged here): this self-dial does NOT actually work, and cannot,
// by construction of go-libp2p itself. go-libp2p's swarm unconditionally
// refuses to dial your own peer ID -- see
// github.com/libp2p/go-libp2p@v0.48.0's p2p/net/swarm/swarm_dial.go:
//
//	if p == s.local {
//		return nil, ErrDialToSelf
//	}
//
// This check fires inside Swarm.dialPeer, reached via
// BasicHost.Connect -> BasicHost.dialPeer -> Network().DialPeer, BEFORE any
// transport (direct or relayed circuit) is even attempted -- it is not a
// timing-sensitive or environment-sensitive race, it is an unconditional
// peer.ID equality check. meshnet.DialPeer calls exactly this Connect path
// before opening its ProtocolID stream, so meshnet.DialPeer(ctx, meshAddr)
// deterministically returns an error wrapping "dial to self attempted"
// whenever meshAddr's target peer ID is this process's own meshnet peer ID
// -- which it always is in this topology, by construction of the singleton
// constraint above. This was confirmed two ways in task 7 (a standalone,
// minimal two-host, non-honey reproduction, and this repo's actual wiring),
// so it is not specific to any bug in this repo's code.
//
// Task 7 originally wrote this test to assert the real, intended success
// outcome and left it failing, documenting the finding above rather than
// weakening the assertion or deleting the test. Task 7b (this version) turns
// that into a permanent, green regression test instead: it asserts the
// specific, documented failure (an error whose message contains
// "dial to self attempted") rather than "no error", so this test would
// actively fail -- loudly telling a future reader something changed -- if
// go-libp2p's swarm ever stops unconditionally rejecting self-dials. If that
// ever happens, it would also mean a genuine single-process, single-Host,
// self-dial-through-relay round trip has become possible, and this test
// (and its doc comment) should be revisited to assert success instead. A
// real two-*distinct*-peer round trip over this same mesh transport is
// separately, positively proven by
// internal/provider/honeyprovider/mesh_e2e_test.go's
// TestHoney_MeshE2E_TwoDistinctPeers (added in task 7b), which swaps
// honeyprovider's meshDial seam to use a genuinely separate third libp2p
// Host as the dialer, sidestepping this singleton constraint entirely.
func TestHoneyProviderMeshE2E_SelfDialIsRejectedByLibp2p(t *testing.T) {
	// 1. Relay host: a raw go-libp2p host acting only as a relay -- a
	// separate, independent libp2p identity from the meshnet singleton
	// below. NOT internal/meshnet: this plays the role of a generic,
	// independent, non-honey libp2p relay node.
	//
	// libp2p.EnableRelayService "configures libp2p to run a circuit v2
	// relay, if we detect that we're publicly reachable" (go doc
	// github.com/libp2p/go-libp2p EnableRelayService). Reading
	// p2p/host/relaysvc/relay.go directly (go doc's one-line summary
	// doesn't spell this out) shows the relay is only actually
	// instantiated when the host's AutoNAT reachability becomes
	// network.ReachabilityPublic (relaysvc.RelayManager watches
	// event.EvtLocalReachabilityChanged and only calls relayv2.New on a
	// Public transition). A loopback-only test host has no real public
	// reachability to auto-detect, so libp2p.ForceReachabilityPublic is
	// required here to make the relay service actually come up --
	// without it, EnableRelayService is silently inert in this
	// environment (go doc github.com/libp2p/go-libp2p ForceReachabilityPublic).
	relayHost, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
		libp2p.EnableRelayService(),
		libp2p.ForceReachabilityPublic(),
	)
	if err != nil {
		t.Fatalf("construct relay host: %v", err)
	}
	t.Cleanup(func() { _ = relayHost.Close() })

	relayAddrs := relayHost.Addrs()
	if len(relayAddrs) == 0 {
		t.Fatal("relay host has no listen addresses")
	}
	relayMultiaddr := relayAddrs[0].String() + "/p2p/" + relayHost.ID().String()

	// 2. This process's mesh identity: the one honey/meshnet singleton in
	// this test process. t.Cleanup unconditionally stops it (even on
	// failure) since it is process-wide global state that would otherwise
	// leak into any other test in this package run afterward in the same
	// binary.
	sk, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate mesh identity key: %v", err)
	}
	rawKey, err := crypto.MarshalPrivateKey(sk)
	if err != nil {
		t.Fatalf("marshal mesh identity key: %v", err)
	}
	privKey := crypto.ConfigEncodeKey(rawKey)

	startCtx, startCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startCancel()
	startErr := meshnet.Start(startCtx, meshnet.Config{
		Enabled:    true,
		PrivateKey: privKey,
		RelayAddrs: []string{relayMultiaddr},
	})
	t.Cleanup(func() { _ = meshnet.Stop(context.Background()) })
	if startErr != nil {
		t.Fatalf("meshnet.Start: %v", startErr)
	}

	// 3. Webserver with mesh enabled: its mesh listener is backed by the
	// same Host from step 2 -- there's only one meshnet singleton, so
	// this is unavoidable and correct (see doc comment above).
	remoteSearchReg := searchrun.NewRegistry([]searchrun.ProviderFactory{
		localprovider.NewFactory(testLocalConfig{}),
	})
	newTestServerOn(t, webserver.Options{
		SearchRegistry: remoteSearchReg,
		Token:          "test-token",
		EnableMesh:     true,
	}, "127.0.0.1")

	// 4. Self-dial circuit address: this Host's own peer ID, reached
	// through the relay from step 1.
	status, err := meshnet.Status()
	if err != nil {
		t.Fatalf("meshnet.Status: %v", err)
	}
	meshAddr := relayMultiaddr + "/p2p-circuit/p2p/" + status.PeerID

	// 5. Drive honeyprovider exactly like TestHoneyProviderE2E /
	// TestHoneyProviderMTLS_E2E: a real config.HoneyBackend with
	// Mesh/MeshAddr set, through honeyprovider.NewFactory/Search.
	//
	// URL intentionally uses "http://", not "https://": the webserver's
	// mesh listener (internal/webserver/server.go's `srv.Serve(meshLn)`)
	// is served by the same plain *http.Server as the ordinary TCP
	// listener -- there is no TLS termination on the mesh path (the
	// libp2p stream itself is already encrypted/authenticated at the
	// transport level by go-libp2p's noise/tls security transport).
	// honeyprovider.buildTransport only assigns a custom DialContext; it
	// never overrides http.Transport's default behavior of performing a
	// TLS client handshake whenever the request URL's scheme is
	// "https" (net/http decides this from the scheme alone, independent
	// of a custom DialContext) -- so an "https://" URL here would make
	// the client attempt a TLS handshake against a plain-HTTP listener
	// and fail for a reason unrelated to the mesh transport itself.
	cfg := &config.File{
		Backends: config.Backends{
			Honey: []config.HoneyBackend{
				{Name: "self-mesh", URL: "http://mesh-self/", Mesh: true, MeshAddr: meshAddr},
			},
		},
	}
	factory := honeyprovider.NewFactory(honeyTestConfig{cfg})
	providers := factory.FromConfig(nil)
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	provider := providers[0]

	// Generous, bounded timeout: a relay reservation + circuit connection
	// is slower than a loopback TCP dial (internal/meshnet's own tests
	// don't exercise a real network at all; this is judgment applied per
	// the brief, 15-30s scale). The self-dial rejection itself is
	// near-instant (an in-memory peer.ID equality check fires before any
	// transport dial), so this bound is generous headroom, not an
	// expectation that this call is slow.
	searchCtx, searchCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer searchCancel()
	recs, err := provider.Search(searchCtx, hosts.Query{NameSubstring: "remote"})
	if err == nil {
		t.Fatalf("expected mesh self-dial to fail with go-libp2p's ErrDialToSelf, but Search succeeded with %d record(s): %+v -- if go-libp2p's swarm has stopped unconditionally rejecting self-dials, update this test (and its doc comment) to assert success instead", len(recs), recs)
	}
	const wantSubstring = "dial to self attempted"
	if !strings.Contains(err.Error(), wantSubstring) {
		t.Fatalf("expected error to contain %q (go-libp2p's ErrDialToSelf, see task-7-report.md), got: %v", wantSubstring, err)
	}
}
