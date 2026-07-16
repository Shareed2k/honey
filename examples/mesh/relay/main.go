// Command relay runs a small, standalone go-libp2p Circuit Relay v2 node.
//
// This is NOT honey code. It's a generic, minimal libp2p relay that any
// honey instance behind NAT/CGNAT can dial through to reach another honey
// instance also behind NAT/CGNAT — see ../README.md and
// website/docs/providers/honey.md's "Mesh (NAT traversal)" section for the
// full picture. An operator could equally run any other correctly
// configured libp2p relay in its place; this is just a convenient, working
// reference implementation.
//
// On startup it prints its own listen multiaddr(s) and peer ID. Copy the
// address that's actually reachable from your honey instances (see
// "Production notes" in ../README.md) into each instance's
// mesh.relay_addrs config, appending "/p2p/<this peer ID>" if it isn't
// already part of the printed address.
//
// Usage:
//
//	go run . [-listen <multiaddr>] [-force-public]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
)

func main() {
	listenAddr := flag.String("listen", "/ip4/0.0.0.0/udp/4001/quic-v1", "multiaddr to listen on")
	identity := flag.String("identity", "", "base64 libp2p private key for a STABLE peer ID across restarts (overrides $RELAY_PRIVATE_KEY). Generate one with the keygen snippet in ../README.md. Leave empty for a random per-run identity (dev only).")
	forcePublic := flag.Bool("force-public", false, "LOCAL TESTING ONLY: force AutoNAT to report this host as publicly reachable, without actually verifying it. Never use this on a real relay -- see 'Production notes' in ../README.md.")
	flag.Parse()

	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(*listenAddr),
		// EnableRelayService configures this host to run a Circuit Relay v2
		// relay -- but go-libp2p only actually activates it once its AutoNAT
		// subsystem decides this host is publicly reachable (see
		// p2p/host/relaysvc/relay.go's RelayManager, which calls
		// relayv2.New only on a transition to network.ReachabilityPublic;
		// see hostctl's own task-7-report.md for the full source trace). For
		// a real relay with a genuine public IP and an open/forwarded UDP
		// port, AutoNAT detects this on its own -- no extra flag needed.
		libp2p.EnableRelayService(),
	}

	// A relay's peer ID is derived from its identity key and is baked into
	// every honey instance's mesh.relay_addrs. For a deployed relay the ID
	// must stay stable across restarts, so take the key from -identity or
	// $RELAY_PRIVATE_KEY (same base64 "config file" encoding honey's own
	// mesh.private_key uses -- see internal/meshnet decodePrivateKey and the
	// keygen snippet in ../README.md). With no key we fall back to a random
	// per-run identity, which is fine for local testing but would change the
	// peer ID on every restart in a deployment.
	keyStr := strings.TrimSpace(os.Getenv("RELAY_PRIVATE_KEY"))
	if *identity != "" {
		keyStr = strings.TrimSpace(*identity)
	}
	if keyStr != "" {
		raw, err := crypto.ConfigDecodeKey(keyStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "decode relay identity key: %v\n", err)
			os.Exit(1)
		}
		sk, err := crypto.UnmarshalPrivateKey(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal relay identity key: %v\n", err)
			os.Exit(1)
		}
		opts = append(opts, libp2p.Identity(sk))
	} else {
		fmt.Fprintln(os.Stderr, "WARNING: no relay identity key set (-identity / $RELAY_PRIVATE_KEY) -- using a random identity. This relay's peer ID will change on every restart and break clients' mesh.relay_addrs. Set a key for a deployable relay (see ../README.md).")
	}

	if *forcePublic {
		// ForceReachabilityPublic overrides AutoNAT and makes this host
		// *believe* it's publicly reachable, without verifying it. This is
		// a test-only shortcut for running the whole client/relay/server
		// topology on one machine (e.g. everything on 127.0.0.1), where
		// AutoNAT has no way to genuinely confirm reachability. A real
		// production relay must NOT use this flag: it needs genuine public
		// reachability, which AutoNAT will detect on its own. Faking it
		// here would make this relay advertise itself as usable even if
		// it's not actually reachable from anywhere.
		opts = append(opts, libp2p.ForceReachabilityPublic())
		fmt.Fprintln(os.Stderr, "WARNING: -force-public is set. This host is NOT verifying real reachability. Do not use this flag for a production relay (see ../README.md's Production notes).")
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "construct libp2p host: %v\n", err)
		os.Exit(1)
	}
	defer h.Close()

	fmt.Println("mesh relay is up. Copy the reachable address below into each honey instance's mesh.relay_addrs:")
	for _, addr := range h.Addrs() {
		fmt.Printf("  %s/p2p/%s\n", addr, h.ID())
	}
	fmt.Printf("peer ID: %s\n", h.ID())
	fmt.Println("press Ctrl+C to stop")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	fmt.Println("shutting down...")
}
