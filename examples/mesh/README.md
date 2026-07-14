# Mesh (NAT traversal) example

Route two honey instances' federation traffic through a libp2p mesh (Circuit
Relay v2 + DCUtR hole-punching) instead of a direct TCP connection, so one
honey server can reach another even when both sit behind NAT/CGNAT with no
port-forward. See [`website/docs/providers/honey.md`](../../website/docs/providers/honey.md)'s
"Mesh (NAT traversal)" section for the config field reference this walks
through.

## Topology

```
honey-client (NAT/CGNAT)                                honey-server (NAT/CGNAT)
config.client.yaml                                       config.server.yaml
        │                                                          │
        │   mesh (libp2p: Circuit Relay v2, then DCUtR             │
        │   hole-punch when possible)                             │
        ▼                                                          ▼
                        ┌────────────────────────────┐
                        │  relay -- public IP, open   │
                        │  UDP port (see relay/)      │
                        └────────────────────────────┘
```

The relay is the only component that needs to be genuinely, publicly
reachable. Both honey instances only ever dial *out* to it (an ordinary
outbound connection, same as either would make through any NAT/CGNAT) and
use it to reach each other. Once a circuit is up, go-libp2p's DCUtR
subsystem tries to upgrade it to a direct, hole-punched connection,
transparently falling back to the relay when that isn't possible.

The relay itself is **not honey code** — it's a small, generic, standalone
libp2p relay (see [`relay/`](relay)). Any correctly configured libp2p relay
works here; `relay/` is just a convenient, working reference implementation
— an operator could equally run some other relay in its place.

## 1. Generate a mesh identity key

Every mesh-participating honey instance needs its own libp2p identity key
(`mesh.private_key`) — and has its own libp2p peer ID, derived from that
key, which the *other* side needs in order to address it. Run this twice:
once for the "server" instance, once for the "client" instance.

```bash
cat > /tmp/mesh-keygen.go <<'EOF'
package main

import (
	"crypto/rand"
	"fmt"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func main() {
	sk, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		panic(err)
	}
	raw, err := crypto.MarshalPrivateKey(sk)
	if err != nil {
		panic(err)
	}
	id, err := peer.IDFromPrivateKey(sk)
	if err != nil {
		panic(err)
	}
	fmt.Println("private_key:", crypto.ConfigEncodeKey(raw))
	fmt.Println("peer_id:", id.String())
}
EOF

# Run this from inside examples/mesh/relay (or any other module/checkout
# that already depends on github.com/libp2p/go-libp2p, e.g. this repo's own
# root) -- `go run` of a standalone file needs the dependency available in
# the current module's build list.
(cd relay && go run /tmp/mesh-keygen.go)
```

Save both lines of output for each instance. `private_key` goes into that
instance's own `mesh.private_key` — it's a secret (see "Production notes"
below). `peer_id` is what the *other* side needs in order to address this
instance — it isn't secret, it's the libp2p equivalent of a public key
fingerprint.

## 2. Run the example relay

```bash
cd relay
go build ./...      # confirms it builds
go run .
```

It prints something like:

```
mesh relay is up. Copy the reachable address below into each honey instance's mesh.relay_addrs:
  /ip4/0.0.0.0/udp/4001/quic-v1/p2p/12D3KooW...
peer ID: 12D3KooW...
press Ctrl+C to stop
```

Run this on a host with a genuine public IP and an open/forwarded UDP port
(4001 by default; override with `-listen`) — see "Production notes" below
for why. If you're just testing the whole setup on one machine, see the
`-force-public` note there too.

Take the printed address, with the real reachable IP substituted for
`0.0.0.0` (e.g. `/ip4/203.0.113.10/udp/4001/quic-v1`) — that's
`<RELAY_MULTIADDR>` below — and the printed peer ID — that's
`<RELAY_PEER_ID>` below.

## 3. Configure both sides

- [`config.server.yaml`](config.server.yaml) — the instance being reached.
  It only needs its own mesh identity (`mesh:` block); it has no
  `backends.honey` entry of its own here.
- [`config.client.yaml`](config.client.yaml) — the instance reaching out. It
  needs its own mesh identity too (every mesh-participating instance dials
  out under its own identity) *plus* a `backends.honey` entry with
  `mesh: true`, pointed at the server's peer ID through the relay.

Replace the placeholders in both files:

| Placeholder | Value | From |
|---|---|---|
| `<RELAY_MULTIADDR>` | the relay's network address, e.g. `/ip4/203.0.113.10/udp/4001/quic-v1` | printed by `relay/` on startup (step 2) |
| `<RELAY_PEER_ID>` | the relay's peer ID | printed by `relay/` on startup (step 2) |
| `<SERVER_PRIVATE_KEY>` | the server instance's own mesh key | step 1, run for the server |
| `<CLIENT_PRIVATE_KEY>` | the client instance's own mesh key | step 1, run for the client |
| `<SERVER_PEER_ID>` | the server instance's own peer ID | step 1, run for the server |

`config.client.yaml`'s `backends.honey[].mesh_addr` is a full circuit-relay
path built from these: `<RELAY_MULTIADDR>/p2p/<RELAY_PEER_ID>/p2p-circuit/p2p/<SERVER_PEER_ID>`.

Start each honey instance with its own config:

```bash
honey web --config config.server.yaml   # on the "server" machine
honey web --config config.client.yaml   # on the "client" machine
```

### Why `http://`, not `https://`, for the mesh backend URL

`config.client.yaml`'s `backends.honey[].url` uses `http://mesh-peer/` —
intentionally, and it isn't a placeholder to fill in. honey's mesh listener
(`internal/webserver`) serves the mesh path with the exact same plain
`*http.Server` as its ordinary TCP listener — there's no TLS termination on
the mesh path, because the libp2p stream itself is already encrypted and
authenticated at the transport level (go-libp2p's own security transport, a
layer separate from the HTTP scheme). `honeyprovider`'s mesh transport only
ever supplies a custom dialer; Go's `net/http.Transport` decides whether to
attempt a TLS handshake purely from the request URL's scheme, independent of
that dialer — so an `https://` URL here would make the client attempt (and
fail) a TLS handshake against a plain-HTTP listener. `url` is only used for
path-building in this mode (the actual network target is `mesh_addr`); it is
never actually resolved over the network.

## 4. Verify

From the client instance (or any machine with a config pointed at it):

```bash
honey search --provider honey --config config.client.yaml -o json
```

A working mesh path returns the server's rows, routed through the relay
(and transparently upgraded to a direct, hole-punched connection when DCUtR
can manage it) exactly like a directly reachable honey backend.

## Production notes

- **The relay needs genuine public reachability, not
  `libp2p.ForceReachabilityPublic()`.** A real relay needs an actual public
  IP and an open/forwarded UDP port; go-libp2p's AutoNAT subsystem detects
  that on its own once it's true, so no extra flag is needed in production.
  `relay/main.go`'s `-force-public` flag exists only to make the whole
  client/relay/server topology work on a single machine for local testing
  (e.g. everything on `127.0.0.1`, where AutoNAT has no way to genuinely
  confirm reachability) — do not use it for a production relay.
- **`mesh.listen_mesh` is normally left off** on honey instances — it makes
  *that* instance also act as a relay for other peers, and is only useful if
  the instance is itself publicly reachable. Most instances behind
  NAT/CGNAT leave it false (the default).
- **`mesh.private_key` is a secret.** Treat it exactly like the `token` or
  `server_ca` fields already documented in
  [`website/docs/providers/honey.md`](../../website/docs/providers/honey.md):
  don't commit real values, and store it the same way you'd store any other
  credential.
