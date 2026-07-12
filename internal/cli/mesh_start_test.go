package cli

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/meshnet"
)

// newMeshTestIdentity returns a fresh ed25519 private key in meshnet.Config's
// "for config file" string format (base64 wrapping a protobuf-serialized
// key), the same encoding internal/meshnet/meshnet_fakes_test.go's
// newTestPrivateKeyString produces for that package's own tests.
func newMeshTestIdentity(t *testing.T) string {
	t.Helper()
	sk, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	raw, err := crypto.MarshalPrivateKey(sk)
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	return crypto.ConfigEncodeKey(raw)
}

// newMeshTestRelayAddr returns a well-formed (parseable) multiaddr for a
// relay that isn't actually listening. Finding 2's fix means Start no longer
// fails just because this relay is unreachable, so an unreachable-but
// well-formed address is all this test needs.
func newMeshTestRelayAddr(t *testing.T) string {
	t.Helper()
	_, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate relay identity: %v", err)
	}
	id, err := peer.IDFromPublicKey(pub)
	if err != nil {
		t.Fatalf("relay peer id: %v", err)
	}
	return "/ip4/127.0.0.1/udp/4001/quic-v1/p2p/" + id.String()
}

// TestStartMeshIfConfigured_SharedCLIInitPoint proves the fix for final
// whole-branch review Finding 1: before this fix, meshnet.Start was wired
// only into internal/cli/web.go's runWeb, so any other command able to reach
// a `mesh: true` honey backend (at minimum `honey search` / `honey exec`,
// which both go through runSearchCore/GetSearchRegistry() without ever
// calling runWeb) never started the mesh singleton — meshnet.DialPeer (via
// honeyprovider's meshDial seam) would always immediately fail with
// "meshnet: not started" for those commands, since internal/meshnet's `cur`
// singleton was never populated.
//
// startMeshIfConfigured (root.go) is the fix: it now runs from rootCmd's
// PersistentPreRunE, which cobra invokes before every subcommand's RunE
// (root.go is the only place in this package defining PersistentPreRunE;
// no subcommand -- search, exec, web, alert, ... -- overrides it), so it is
// exercised on the shared path all of those commands funnel through, not
// just web's.
//
// This test calls startMeshIfConfigured directly (rather than
// rootCmd.PersistentPreRunE, which also touches flags/logger/real config
// file resolution -- more than this fix needs to prove) with a mesh-enabled
// config, and asserts that the mesh singleton actually comes up: Status /
// DialPeer no longer immediately fail with meshnet's "not started" error the
// way they would have before this fix (or after it, for any command that
// forgot to call it). A real (but deliberately non-listening) libp2p Host is
// constructed -- the same real-Host pattern already used by
// tests/integration/honey_provider_mesh_e2e_test.go and
// internal/provider/honeyprovider/mesh_e2e_test.go -- because meshnet's
// newHost seam is package-private to internal/meshnet and can't be faked
// from here; this is the CLI-level integration seam this finding is about.
func TestStartMeshIfConfigured_SharedCLIInitPoint(t *testing.T) {
	// internal/meshnet is a process-wide singleton (see its Start/Stop
	// idempotency contract); reset it before and after so this test can't
	// leak state into, or be affected by state left over from, any other
	// test in this binary.
	_ = meshnet.Stop(context.Background())
	t.Cleanup(func() { _ = meshnet.Stop(context.Background()) })

	if _, err := meshnet.Status(); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatalf("precondition: Status() before any Start = %v, want a not-started error", err)
	}

	cfg := &config.File{
		Mesh: config.MeshConfig{
			Enabled:    true,
			PrivateKey: newMeshTestIdentity(t),
			RelayAddrs: []string{newMeshTestRelayAddr(t)},
		},
	}

	startMeshIfConfigured(cfg)

	if !meshnet.Enabled() {
		t.Fatal("expected meshnet.Enabled() to be true after startMeshIfConfigured with a mesh-enabled config")
	}
	if _, err := meshnet.Status(); err != nil {
		t.Fatalf("Status() after startMeshIfConfigured: got %v, want nil (mesh singleton must actually be up, not just cfg.Enabled cached)", err)
	}

	dialCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, dialErr := meshnet.DialPeer(dialCtx, newMeshTestRelayAddr(t))
	if dialErr == nil {
		t.Fatal("expected DialPeer to a nonexistent peer to fail (for a real reason)")
	}
	if strings.Contains(dialErr.Error(), "not started") {
		t.Fatalf("DialPeer after startMeshIfConfigured: got %v, want any error EXCEPT meshnet's not-started error (the exact regression Finding 1 fixes)", dialErr)
	}
}

// TestStartMeshIfConfigured_NilOrDisabledConfigIsNoOp confirms
// startMeshIfConfigured leaves the mesh singleton untouched (never even
// calls meshnet.Start) when there is no config or mesh isn't enabled --
// mirroring web.go's pre-existing `cfg != nil && cfg.Mesh.Enabled` guard, now
// shared by every command via root.go's PersistentPreRunE instead of being
// web-only.
func TestStartMeshIfConfigured_NilOrDisabledConfigIsNoOp(t *testing.T) {
	_ = meshnet.Stop(context.Background())
	t.Cleanup(func() { _ = meshnet.Stop(context.Background()) })

	startMeshIfConfigured(nil)
	if meshnet.Enabled() {
		t.Fatal("expected Enabled() to be false after startMeshIfConfigured(nil)")
	}

	startMeshIfConfigured(&config.File{Mesh: config.MeshConfig{Enabled: false}})
	if meshnet.Enabled() {
		t.Fatal("expected Enabled() to be false after startMeshIfConfigured with Mesh.Enabled=false")
	}
}
